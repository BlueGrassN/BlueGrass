//go:build windows

package winservice

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bluegrass/native/core/cmdupdate"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/nativehost"
	"bluegrass/native/core/serviceipc"
	"bluegrass/native/core/update"
	"bluegrass/native/core/winprotect"
	"golang.org/x/sys/windows/svc"
)

const Name = "BlueGrassService"

type service struct{}

func Run() error { return svc.Run(Name, &service{}) }
func (*service) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	root, publisher, err := serviceipc.Installation()
	if err != nil {
		return true, 1
	}
	exe, err := os.Executable()
	if err != nil {
		return true, 1
	}
	protection, err := winprotect.New(exe, filepath.Join(root, "BlueGrass.exe"))
	if err != nil {
		return true, 2
	}
	defer protection.Close()
	// Only the exact installed updater with the installation's trusted signer
	// receives a TCP443 app exception or the separate read-only resolver role.
	helperReady := update.VerifyPublisher(filepath.Join(root, "BlueGrass.Update.exe"), publisher) == nil
	if helperReady && protection.AllowUpdateHelper() != nil {
		helperReady = false
	}
	listener, err := serviceipc.Listen()
	if err != nil {
		return true, 3
	}
	var clients sync.Map
	var workers sync.WaitGroup
	var sessions clientLifecycle
	slots := make(chan struct{}, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				conn.Close()
				continue
			}
			clients.Store(conn, struct{}{})
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-slots }()
				defer clients.Delete(conn)
				defer conn.Close()
				if helperReady {
					if identity, err := serviceipc.AuthenticateUpdateHelper(conn, root, publisher, update.TrustedPublisher); err == nil {
						resolver := newProtectedTunnel(protection, identity.SID, filepath.Join(root, "BlueGrass.exe"))
						// No host, engine lease or Close: this object only holds the
						// explicit resolver dependencies and never owns a TUN.
						_ = serveUpdateResolver(conn, cmdupdate.TrustedHosts(), func(ctx context.Context, host string) ([]netip.Addr, error) {
							return resolver.resolveUpdateHost(ctx, host, protection.OwnerSID())
						})
						return
					}
				}
				identity, err := serviceipc.Authenticate(conn, root, publisher, update.TrustedPublisher, false)
				if err != nil {
					return
				}
				if err := sessions.acquire(identity); err != nil {
					code := "SERVICE_BUSY"
					if errors.Is(err, errCleanupPending) {
						code = "RECOVERY_REQUIRED"
					}
					rejectFirst(conn, code)
					return
				}
				var closeOwner func() error
				defer func() { sessions.retire(identity, closeOwner) }()
				if protection.Active() && protection.OwnerSID() != identity.SID {
					rejectFirst(conn, "OWNER_MISMATCH")
					return
				}
				network := newProtectedTunnel(protection, identity.SID, filepath.Join(root, "BlueGrass.exe"))
				host := nativehost.NewTunnel(network)
				closeOwner = host.Close
				_ = serve(conn, host, protection, identity.SID, network)
			}()
		}
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case <-done:
			clients.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
			workers.Wait()
			return true, 4
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 15000}
				listener.Close()
				<-done
				clients.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
				workers.Wait()
				// SCM stop/update/shutdown never means explicit protection release.
				if errors.Join(sessions.retryCleanup(), protection.Block()) != nil {
					return true, 5
				}
				return false, 0
			}
		}
	}
}

func rejectFirst(conn net.Conn, code string) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	b, err := serviceipc.ReadFrame(conn)
	if err != nil {
		return
	}
	r, err := nativehost.Decode(b)
	if err != nil {
		return
	}
	data, _ := json.Marshal(nativehost.Response{ID: r.ID, OK: false, Error: code})
	_ = serviceipc.WriteFrame(conn, data)
}
func serve(conn net.Conn, host *nativehost.Host, protection *winprotect.Manager, sid string, network *protectedTunnel) error {
	hello := false
	for {
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
		b, err := serviceipc.ReadFrame(conn)
		if err != nil {
			return err
		}
		r, err := nativehost.Decode(b)
		if err != nil {
			return err
		}
		if network.networkChanged() {
			if host.NetworkChanged() != nil {
				return errors.New("NETWORK_PAUSE_FAILED")
			}
		}
		if !hello && r.Command != "Hello" {
			return errors.New("HELLO_REQUIRED")
		}
		hello = true
		var reply nativehost.Response
		if r.Command == "Connect" && protection.Active() {
			var p engine.Profile
			if json.Unmarshal(r.Profile, &p) != nil || !protection.Authorized(sid, p.Key.AccountKey, p.Key.SourceID) {
				reply = nativehost.Response{ID: r.ID, OK: false, Error: "OWNER_MISMATCH"}
			}
		}
		if reply.Error == "" {
			reply = host.Handle(r)
		}
		data, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		if err = serviceipc.WriteFrame(conn, data); err != nil {
			return err
		}
	}
}

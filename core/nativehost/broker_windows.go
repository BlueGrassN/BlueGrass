//go:build windows

package nativehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/serviceipc"
	"bluegrass/native/core/update"
	"golang.org/x/sys/windows"
)

// RunTunnelBroker carries private inherited-pipe messages over an authenticated
// framed service pipe. EOF only closes the pipe: the service pauses forwarding
// and retains WFP. Explicit Disconnect is the sole normal release operation.
func RunTunnelBroker(input io.Reader, output io.Writer) error {
	return runTunnel(input, output, nil)
}

type tunnelClient interface {
	Exchange([]byte) ([]byte, error)
	Close() error
}

func runTunnel(input io.Reader, output io.Writer, client tunnelClient) error {
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), serviceipc.MaxFrame+1)
	for scanner.Scan() {
		data := scanner.Bytes()
		r, err := Decode(data)
		if err != nil {
			return err
		}
		if client == nil {
			if r.Command != "Hello" {
				return errors.New("HELLO_REQUIRED")
			}
			root, publisher, initErr := serviceipc.Installation()
			if initErr == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var connected *serviceipc.Client
				connected, initErr = serviceipc.Dial(ctx, root, publisher, update.TrustedPublisher)
				cancel()
				if initErr == nil {
					client = connected
				}
			}
			if initErr != nil {
				reply, _ := json.Marshal(Response{ID: r.ID, OK: false, Error: "SERVICE_UNAVAILABLE"})
				_, _ = output.Write(append(reply, '\n'))
				return initErr
			}
		}
		reply, err := client.Exchange(data)
		if err != nil {
			return errors.New("SERVICE_TRANSPORT_FAILED")
		}
		if _, err = output.Write(append(reply, '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// An installed system-proxy launch must also discover a previous TUN intent.
// A missing user preference cannot silently release or hide service protection.
func recoverTunnelIfPresent(input io.Reader, output io.Writer) (bool, error) {
	return recoverTunnel(input, output, recoveryDependencies{
		installed:    currentInstalledLocation,
		installation: serviceipc.Installation,
		dial: func(ctx context.Context, root, publisher string) (tunnelClient, error) {
			client, err := serviceipc.Dial(ctx, root, publisher, update.TrustedPublisher)
			if err != nil {
				return nil, err
			}
			return client, nil
		},
	})
}

type recoveryDependencies struct {
	installed    func() (bool, error)
	installation func() (string, string, error)
	dial         func(context.Context, string, string) (tunnelClient, error)
}

func recoverTunnel(input io.Reader, output io.Writer, deps recoveryDependencies) (bool, error) {
	fail := func(code string) (bool, error) {
		return true, recoveryFailure(input, output, code)
	}
	installed, err := deps.installed()
	if err != nil {
		return fail("SERVICE_UNAVAILABLE")
	}
	if !installed {
		return false, nil
	}
	root, publisher, err := deps.installation()
	if err != nil {
		return fail("SERVICE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := deps.dial(ctx, root, publisher)
	if err != nil {
		return fail("SERVICE_UNAVAILABLE")
	}
	request := Request{ID: randomID(), Command: "Hello"}
	probe, _ := json.Marshal(request)
	data, err := client.Exchange(probe)
	if err != nil {
		_ = client.Close()
		return fail("RECOVERY_REQUIRED")
	}
	var reply Response
	if control.ValidateJSON(data) != nil || json.Unmarshal(data, &reply) != nil || reply.ID != request.ID {
		_ = client.Close()
		return fail("RECOVERY_REQUIRED")
	}
	if !reply.OK {
		_ = client.Close()
		code := "RECOVERY_REQUIRED"
		switch reply.Error {
		case "OWNER_MISMATCH", "SERVICE_BUSY", "SERVICE_UNAVAILABLE":
			code = reply.Error
		}
		return fail(code)
	}
	intent, known := reply.Snapshot["protectionIntent"].(bool)
	if !known || reply.Error != "" {
		_ = client.Close()
		return fail("RECOVERY_REQUIRED")
	}
	if !intent {
		_ = client.Close()
		if reply.Snapshot["gateKnown"] != true || reply.Snapshot["businessGateOpen"] != false ||
			reply.Snapshot["connection"] != "disconnected" || reply.Snapshot["actualMode"] != "none" {
			return fail("RECOVERY_REQUIRED")
		}
		return false, nil
	}
	return true, runTunnel(input, output, client)
}

// Report startup uncertainty against the real GUI Hello, not the private probe
// ID. No disconnected snapshot is fabricated when service recovery is unknown.
func recoveryFailure(input io.Reader, output io.Writer, code string) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), serviceipc.MaxFrame+1)
	if !scanner.Scan() {
		return errors.Join(errors.New(code), scanner.Err())
	}
	r, err := Decode(scanner.Bytes())
	if err != nil {
		return err
	}
	if r.Command != "Hello" || r.ID == "" || len(r.ID) > 128 {
		return errors.New("HELLO_REQUIRED")
	}
	reply, _ := json.Marshal(Response{ID: r.ID, OK: false, Error: code})
	_, err = output.Write(append(reply, '\n'))
	return errors.Join(errors.New(code), err)
}

func currentInstalledLocation() (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return false, err
	}
	return installedLocation(exe, programs, filepath.EvalSymlinks)
}

// Path classification deliberately precedes signer verification. An invalid
// installed binary must never gain developer fallback by failing Installation.
func installedLocation(exe, programs string, resolve func(string) (string, error)) (bool, error) {
	if !filepath.IsAbs(exe) || !filepath.IsAbs(programs) {
		return false, errors.New("INSTALLATION_UNKNOWN")
	}
	root := filepath.Join(programs, "BlueGrass")
	within := func(path, directory string) bool {
		path, directory = strings.ToLower(filepath.Clean(path)), strings.ToLower(filepath.Clean(directory))
		return path == directory || strings.HasPrefix(path, directory+string(filepath.Separator))
	}
	if within(exe, root) {
		return true, nil
	}
	exe, err := resolve(exe)
	if err != nil {
		return false, err
	}
	if within(exe, root) {
		return true, nil
	}
	resolvedRoot, err := resolve(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return within(exe, resolvedRoot), nil
}

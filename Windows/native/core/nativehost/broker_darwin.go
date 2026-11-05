//go:build darwin

package nativehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/mactunipc"
	"golang.org/x/sys/unix"
)

const darwinStartupTimeout = 115 * time.Second

// RunTunnelBroker handles only the GUI's existing private inherited stream.
// Administrator passwords stay in the system dialog; profiles only cross the
// authenticated local socket. EOF cancels authorization and closes the worker.
func RunTunnelBroker(input io.Reader, output io.Writer) error {
	return runDarwinTunnel(input, output, nil)
}

type darwinTunnelClient interface {
	Exchange([]byte) ([]byte, error)
	Close() error
}

func darwinBrokerCode(err error) string {
	for _, known := range []error{mactunipc.ErrAuthorization, mactunipc.ErrAuthorizationTimeout, mactunipc.ErrHelperInvalid, mactunipc.ErrBusy, mactunipc.ErrUnavailable, mactunipc.ErrRecovery} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return mactunipc.ErrUnavailable.Error()
}

func darwinBrokerFailure(output io.Writer, id string, err error) error {
	code := darwinBrokerCode(err)
	b, _ := json.Marshal(Response{ID: id, OK: false, Error: code})
	_, writeErr := output.Write(append(b, '\n'))
	return errors.Join(errors.New(code), writeErr)
}

func runDarwinTunnel(input io.Reader, output io.Writer, client darwinTunnelClient) error {
	return runDarwinTunnelBefore(input, output, client, time.Now().Add(darwinStartupTimeout))
}

func runDarwinTunnelBefore(input io.Reader, output io.Writer, client darwinTunnelClient, startupDeadline time.Time) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stopStartup func() bool
	var cancelStartup context.CancelFunc
	defer func() {
		if stopStartup != nil {
			stopStartup()
		}
	}()
	var stopClient func() bool
	defer func() {
		if stopClient != nil {
			stopClient()
		}
		if client != nil {
			_ = client.Close()
		}
	}()
	if client != nil {
		connected := client
		stopClient = context.AfterFunc(ctx, func() { _ = connected.Close() })
	}
	requests := make(chan []byte, 1)
	readErr := make(chan error, 1)
	// Read while the system dialog is open so a vanished GUI revokes pending
	// authorization. The queue is bounded; never retain a profile backlog.
	go func() {
		s := bufio.NewScanner(input)
		s.Buffer(make([]byte, 4096), mactunipc.MaxFrame+2)
		defer cancel()
		defer close(requests)
		for s.Scan() {
			data := append([]byte(nil), s.Bytes()...)
			if len(data) > mactunipc.MaxFrame {
				readErr <- mactunipc.ErrUnavailable
				return
			}
			select {
			case requests <- data:
			case <-ctx.Done():
				readErr <- ctx.Err()
				return
			default:
				readErr <- mactunipc.ErrUnavailable
				return
			}
		}
		readErr <- s.Err()
	}()
	first := true
	for data := range requests {
		r, err := Decode(data)
		if err != nil {
			return err
		}
		if r.ID == "" || len(r.ID) > 128 || (first && r.Command != "Hello") {
			return errors.New("HELLO_REQUIRED")
		}
		opening := ctx
		if first {
			startup, startupCancel := context.WithDeadline(ctx, startupDeadline)
			defer startupCancel()
			opening, cancelStartup = startup, startupCancel
		}
		first = false
		if client == nil {
			client, err = mactunipc.Open(opening)
			if err != nil {
				return darwinBrokerFailure(output, r.ID, err)
			}
			connected := client
			stopClient = context.AfterFunc(ctx, func() { _ = connected.Close() })
		}
		if cancelStartup != nil {
			connected := client
			stopStartup = context.AfterFunc(opening, func() { _ = connected.Close() })
		}
		if ctx.Err() != nil {
			return nil
		}
		reply, exchangeErr := client.Exchange(data)
		if stopStartup != nil {
			stopStartup()
			stopStartup = nil
		}
		if cancelStartup != nil {
			cancelStartup()
			cancelStartup = nil
		}
		if exchangeErr != nil {
			return darwinBrokerFailure(output, r.ID, exchangeErr)
		}
		var response Response
		if control.ValidateJSON(reply) != nil || json.Unmarshal(reply, &response) != nil || response.ID != r.ID {
			return darwinBrokerFailure(output, r.ID, mactunipc.ErrUnavailable)
		}
		if _, err = output.Write(append(reply, '\n')); err != nil {
			return err
		}
	}
	return <-readErr
}

// recoverDarwinTunnelIfPresent is called before system-proxy initialization by
// the lead-owned composition. It never reads or wraps input when returning false.
func recoverDarwinTunnelIfPresent(input io.Reader, output io.Writer) (bool, error) {
	return recoverDarwinTunnel(input, output, darwinRecoveryDependencies{
		intent: mactunipc.ReadIntent,
		open:   func(ctx context.Context) (darwinTunnelClient, error) { return mactunipc.Open(ctx) },
	})
}

type darwinRecoveryDependencies struct {
	intent func() (*mactunipc.Intent, error)
	open   func(context.Context) (darwinTunnelClient, error)
}

func recoverDarwinTunnel(input io.Reader, output io.Writer, deps darwinRecoveryDependencies) (bool, error) {
	intent, err := deps.intent()
	if err != nil {
		return true, darwinRecoveryFailure(input, output, mactunipc.ErrRecovery)
	}
	if intent == nil {
		return false, nil
	}
	if intent.OwnerUID != uint32(os.Getuid()) {
		return true, darwinRecoveryFailure(input, output, mactunipc.ErrRecovery)
	}
	// Poll the inherited pipe for HUP without reading any bytes. A buffered
	// Hello stays on the original reader if an inactive probe permits fallback.
	ctx, stopMonitor, err := monitorDarwinRecoveryInput(input)
	if err != nil {
		return true, darwinRecoveryFailure(input, output, mactunipc.ErrUnavailable)
	}
	defer stopMonitor()
	// Open retains its own 75-second cap. This outer budget also bounds the
	// probe/initial manager work and leaves cleanup time inside GUI Hello's 120s.
	startup, cancel := context.WithTimeout(ctx, darwinStartupTimeout)
	defer cancel()
	client, err := deps.open(startup)
	if err != nil {
		if ctx.Err() != nil {
			return true, nil
		} // GUI is gone; do not await another Hello.
		return true, darwinRecoveryFailure(input, output, err)
	}
	stopClient := context.AfterFunc(startup, func() { _ = client.Close() })
	defer stopClient()
	if startup.Err() != nil {
		_ = client.Close()
		if ctx.Err() != nil {
			return true, nil
		}
		return true, darwinRecoveryFailure(input, output, mactunipc.ErrUnavailable)
	}
	probe, _ := json.Marshal(Request{ID: randomID(), Command: "Hello"})
	data, err := client.Exchange(probe)
	var request Request
	_ = json.Unmarshal(probe, &request)
	var reply Response
	if err != nil || control.ValidateJSON(data) != nil || json.Unmarshal(data, &reply) != nil || !reply.OK || reply.ID != request.ID || reply.Error != "" {
		_ = client.Close()
		if ctx.Err() != nil {
			return true, nil
		}
		return true, darwinRecoveryFailure(input, output, mactunipc.ErrRecovery)
	}
	active, known := reply.Snapshot["protectionIntent"].(bool)
	if !known {
		_ = client.Close()
		return true, darwinRecoveryFailure(input, output, mactunipc.ErrRecovery)
	}
	if !active {
		_ = client.Close()
		if reply.Snapshot["gateKnown"] != true || reply.Snapshot["businessGateOpen"] != false || reply.Snapshot["connection"] != "disconnected" || reply.Snapshot["actualMode"] != "none" {
			return true, darwinRecoveryFailure(input, output, mactunipc.ErrRecovery)
		}
		return false, nil
	}
	// The normal bounded request reader takes over EOF monitoring. Stop the
	// startup-only deadline so it cannot kill a later 100-second Connect.
	// Carry the remaining original deadline through the real GUI Hello.
	startupDeadline, _ := startup.Deadline()
	stopClient()
	cancel()
	stopMonitor()
	return true, runDarwinTunnelBefore(input, output, client, startupDeadline)
}

// The production recovery source is an inherited os.Stdin pipe. Requiring a
// kernel-backed stream here avoids consuming arbitrary Reader data to discover
// EOF and then losing the original Hello on a false/inactive return. Unsupported
// recovery sources fail closed; the no-intent path supports any io.Reader.
func monitorDarwinRecoveryInput(input io.Reader) (context.Context, func(), error) {
	stream, ok := input.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return nil, nil, mactunipc.ErrUnavailable
	}
	raw, err := stream.SyscallConn()
	if err != nil {
		return nil, nil, mactunipc.ErrUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			var pollErr error
			var dead bool
			err := raw.Control(func(fd uintptr) {
				if fd > 0x7fffffff {
					pollErr = mactunipc.ErrUnavailable
					return
				}
				// HUP/ERR/NVAL are reported independently of requested events.
				// Do not request POLLIN: queued Hello bytes must not busy-loop.
				fds := []unix.PollFd{{Fd: int32(fd)}}
				_, pollErr = unix.Poll(fds, 50)
				dead = fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0
			})
			if err != nil || (pollErr != nil && pollErr != unix.EINTR) || dead {
				cancel()
				return
			}
		}
	}()
	return ctx, func() { cancel(); <-done }, nil
}

func darwinRecoveryFailure(input io.Reader, output io.Writer, cause error) error {
	s := bufio.NewScanner(input)
	s.Buffer(make([]byte, 4096), mactunipc.MaxFrame+2)
	if !s.Scan() {
		return errors.Join(errors.New(darwinBrokerCode(cause)), s.Err())
	}
	if len(s.Bytes()) > mactunipc.MaxFrame {
		return mactunipc.ErrUnavailable
	}
	r, err := Decode(s.Bytes())
	if err != nil {
		return err
	}
	if r.Command != "Hello" || r.ID == "" || len(r.ID) > 128 {
		return errors.New("HELLO_REQUIRED")
	}
	return darwinBrokerFailure(output, r.ID, cause)
}

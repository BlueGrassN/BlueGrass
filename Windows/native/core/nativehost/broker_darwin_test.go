//go:build darwin

package nativehost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/mactunipc"
)

type recoveryInputGuard struct{}

func (recoveryInputGuard) Read([]byte) (int, error) {
	panic("absent intent consumed system-proxy input")
}

func TestDarwinRecoveryAbsentIntentDoesNotReadOrAuthorize(t *testing.T) {
	handled, err := recoverDarwinTunnel(recoveryInputGuard{}, io.Discard, darwinRecoveryDependencies{
		intent: func() (*mactunipc.Intent, error) { return nil, nil },
		open: func(context.Context) (darwinTunnelClient, error) {
			t.Fatal("authorization without intent")
			return nil, nil
		},
	})
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func TestDarwinRecoveryCancelsPendingAuthorizationOnGUIHangup(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	hello := []byte("{\"id\":\"gui\",\"command\":\"Hello\"}\n")
	if _, err = w.Write(hello); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := recoverDarwinTunnel(r, io.Discard, darwinRecoveryDependencies{
			intent: func() (*mactunipc.Intent, error) {
				return &mactunipc.Intent{Version: 1, OwnerUID: uint32(os.Getuid()), Active: true}, nil
			},
			open: func(ctx context.Context) (darwinTunnelClient, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		done <- err
	}()
	<-started
	_ = w.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("authorization outlived GUI death")
	}
	got := make([]byte, len(hello))
	if _, err = io.ReadFull(r, got); err != nil || !bytes.Equal(got, hello) {
		t.Fatal("hangup monitor consumed buffered Hello")
	}
}

type inactiveDarwinClient struct {
	closed chan struct{}
	once   sync.Once
}

func (c *inactiveDarwinClient) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

type pendingDarwinProbe struct {
	inactiveDarwinClient
	begun chan struct{}
}

func (c *pendingDarwinProbe) Exchange([]byte) ([]byte, error) {
	close(c.begun)
	<-c.closed
	return nil, mactunipc.ErrUnavailable
}

func TestDarwinRecoveryHangupClosesProbeAndRejectsLateApproval(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending probe", true: "late approval"}[late], func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			c := &pendingDarwinProbe{inactiveDarwinClient: inactiveDarwinClient{closed: make(chan struct{})}, begun: make(chan struct{})}
			approval := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := recoverDarwinTunnel(r, io.Discard, darwinRecoveryDependencies{
					intent: func() (*mactunipc.Intent, error) {
						return &mactunipc.Intent{Version: 1, OwnerUID: uint32(os.Getuid()), Active: true}, nil
					},
					open: func(ctx context.Context) (darwinTunnelClient, error) {
						close(approval)
						if late {
							<-ctx.Done()
						}
						return c, nil
					},
				})
				done <- err
			}()
			<-approval
			if !late {
				<-c.begun
			}
			_ = w.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("GUI hangup retained root probe")
			}
			select {
			case <-c.closed:
			default:
				t.Fatal("GUI hangup did not close late/probe client")
			}
			if late {
				select {
				case <-c.begun:
					t.Fatal("late approval started a probe after GUI death")
				default:
				}
			}
		})
	}
}
func (c *inactiveDarwinClient) Exchange(data []byte) ([]byte, error) {
	var r Request
	_ = json.Unmarshal(data, &r)
	return json.Marshal(Response{ID: r.ID, OK: true, Snapshot: map[string]any{"protectionIntent": false, "gateKnown": true, "businessGateOpen": false, "connection": "disconnected", "actualMode": "none"}})
}

func TestDarwinRecoveryInactiveProbePreservesOriginalPipeBytes(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	hello := []byte("{\"id\":\"gui\",\"command\":\"Hello\"}\n")
	if _, err = w.Write(hello); err != nil {
		t.Fatal(err)
	}
	c := &inactiveDarwinClient{closed: make(chan struct{})}
	handled, err := recoverDarwinTunnel(r, io.Discard, darwinRecoveryDependencies{
		intent: func() (*mactunipc.Intent, error) {
			return &mactunipc.Intent{Version: 1, OwnerUID: uint32(os.Getuid())}, nil
		},
		open: func(context.Context) (darwinTunnelClient, error) { return c, nil },
	})
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	got := make([]byte, len(hello))
	if _, err = io.ReadFull(r, got); err != nil || !bytes.Equal(got, hello) {
		t.Fatalf("fallback lost Hello: %q %v", got, err)
	}
	select {
	case <-c.closed:
	default:
		t.Fatal("inactive probe retained client")
	}
}

func TestDarwinBrokerStartupHonorsRemainingRecoveryDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	_, _ = w.Write([]byte("{\"id\":\"h\",\"command\":\"Hello\"}\n"))
	c := &pendingDarwinProbe{inactiveDarwinClient: inactiveDarwinClient{closed: make(chan struct{})}, begun: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- runDarwinTunnelBefore(r, io.Discard, c, time.Now().Add(40*time.Millisecond)) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired startup succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("recovery handoff restarted startup budget")
	}
	select {
	case <-c.closed:
	default:
		t.Fatal("startup deadline retained root client")
	}
}

type stagedDarwinClient struct {
	inactiveDarwinClient
	hello, connected, release chan struct{}
}

func (c *stagedDarwinClient) Exchange(data []byte) ([]byte, error) {
	var request Request
	_ = json.Unmarshal(data, &request)
	if request.Command == "Hello" {
		close(c.hello)
	}
	if request.Command == "Connect" {
		close(c.connected)
		select {
		case <-c.release:
		case <-c.closed:
			return nil, mactunipc.ErrUnavailable
		}
	}
	return json.Marshal(Response{ID: request.ID, OK: true})
}

func TestDarwinBrokerStartupTimerDoesNotLimitEstablishedOperation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	c := &stagedDarwinClient{inactiveDarwinClient: inactiveDarwinClient{closed: make(chan struct{})}, hello: make(chan struct{}), connected: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- runDarwinTunnelBefore(r, io.Discard, c, time.Now().Add(300*time.Millisecond)) }()
	_, _ = w.Write([]byte("{\"id\":\"h\",\"command\":\"Hello\"}\n"))
	select {
	case <-c.hello:
	case <-time.After(time.Second):
		t.Fatal("Hello did not start")
	}
	_, _ = w.Write([]byte("{\"id\":\"c\",\"command\":\"Connect\"}\n"))
	select {
	case <-c.connected:
	case <-time.After(time.Second):
		t.Fatal("Connect did not start")
	}
	select {
	case <-c.closed:
		t.Fatal("startup timer truncated established operation")
	case <-time.After(350 * time.Millisecond):
	}
	close(c.release)
	_ = w.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("established session did not close on EOF")
	}
}

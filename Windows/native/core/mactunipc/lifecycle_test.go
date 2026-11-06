package mactunipc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestSessionOwnerDiesClosesForwarding(t *testing.T) {
	owner := Identity{501, 42, 123, 456}
	a, b := net.Pipe()
	defer b.Close()
	alive := make(chan struct{})
	closed := make(chan struct{})
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveOwned(context.Background(), a, owner, func(Identity) bool {
			select {
			case <-alive:
				return false
			default:
				return true
			}
		}, func(r io.Reader, _ io.Writer, _ uint32) error {
			close(started)
			defer close(closed)
			_, err := io.Copy(io.Discard, r)
			return err
		})
	}()
	<-started
	close(alive)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("dead owner retained forwarding")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dead owner retained server")
	}
}

func TestDeadOwnerNeverStartsHost(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	err := serveOwned(context.Background(), a, Identity{501, 42, 123, 456}, func(Identity) bool { return false }, func(io.Reader, io.Writer, uint32) error { t.Fatal("late approval started host"); return nil })
	if err == nil {
		t.Fatal("dead owner accepted")
	}
}

func TestClientDeadlineClosesBlockedExchange(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := &Client{conn: a, timeout: 20 * time.Millisecond}
	if _, err := c.Exchange([]byte(`{"id":"x","command":"Hello"}`)); err == nil {
		t.Fatal("blocked write had no deadline")
	}
	if _, err := c.Exchange([]byte(`{"id":"x"}`)); err == nil {
		t.Fatal("failed connection was reused")
	}
}

func TestAuthorizationErrorCodes(t *testing.T) {
	for _, tt := range []struct {
		s   string
		err error
	}{
		{"AUTHORIZATION_CANCELLED", ErrAuthorization},
		{"AUTHORIZATION_TIMEOUT", ErrAuthorizationTimeout},
		{"PRIVILEGED_HELPER_INVALID", ErrHelperInvalid},
		{"SERVICE_BUSY", ErrBusy},
		{"arbitrary path or shell diagnostic", ErrUnavailable},
	} {
		if !errors.Is(authorizationResult(tt.s), tt.err) {
			t.Fatal(tt.s)
		}
	}
}

func TestAuthorizationBudgetLeavesTimeForGUIHandshake(t *testing.T) {
	ctx, cancel := authorizationContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 75*time.Second || time.Until(deadline) <= 0 {
		t.Fatalf("authorization deadline %v", deadline)
	}
	parent, stop := context.WithCancel(context.Background())
	short, done := authorizationContext(parent)
	defer done()
	stop()
	select {
	case <-short.Done():
	case <-time.After(time.Second):
		t.Fatal("authorization ignored parent cancellation")
	}
}

func TestWorkerArgumentsRejectPathsAndOverflow(t *testing.T) {
	valid := []string{"--owner-uid", "501", "--owner-pid", "42", "--owner-start-sec", "123", "--owner-start-usec", "456"}
	if _, err := ParseOwner(valid); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"-1", "+501", "501;bad", "4294967296", "0", ""} {
		a := append([]string(nil), valid...)
		a[1] = v
		if _, err := ParseOwner(a); err == nil {
			t.Fatal(v)
		}
	}
	a := append([]string(nil), valid...)
	a[7] = "1000000"
	if _, err := ParseOwner(a); err == nil {
		t.Fatal("invalid microseconds")
	}
	if _, err := ParseOwner(append(valid, "--socket", "/tmp/bad")); err == nil {
		t.Fatal("arbitrary socket accepted")
	}
}

func TestWorkerInfoContainsOnlyPublicBuildData(t *testing.T) {
	old := ExpectedWorkerSHA256
	defer func() { ExpectedWorkerSHA256 = old }()
	ExpectedWorkerSHA256 = "abcdef"
	if !bytes.Equal(WorkerInfo(), []byte("{\"expectedWorkerSHA256\":\"abcdef\",\"protocolVersion\":1}\n")) {
		t.Fatalf("%s", WorkerInfo())
	}
}

func TestUnresponsiveHostCannotRetainPrivilegedProcess(t *testing.T) {
	a, b := net.Pipe()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ServeStream(context.Background(), a, 501, func(io.Reader, io.Writer, uint32) error { close(started); <-release; return nil })
	}()
	<-started
	_ = b.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("expected forced shutdown error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		<-done
		t.Fatal("host operation retained worker after EOF")
	}
	close(release)
}

package mactunipc

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestOperationCanOutliveIdleBudgetAndIdleResumesAfterReply(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		done <- serveStream(context.Background(), a, 501, func(r io.Reader, w io.Writer, _ uint32) error {
			s := bufio.NewScanner(r)
			for s.Scan() {
				data := append([]byte(nil), s.Bytes()...)
				time.Sleep(80 * time.Millisecond)
				if _, err := w.Write(append(data, '\n')); err != nil {
					return err
				}
			}
			return s.Err()
		}, streamTimeouts{idle: 20 * time.Millisecond, operation: 300 * time.Millisecond, cleanup: time.Second})
	}()
	c := &Client{conn: b, timeout: time.Second}
	for range 2 {
		if _, err := c.Exchange([]byte(`{"id":"c","command":"Connect"}`)); err != nil {
			t.Fatalf("operation was cut off by idle timeout: %v", err)
		}
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("idle timeout was lost")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle timeout did not resume after reply")
	}
}

func TestOperationDeadlineClosesHostWithoutReply(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		done <- serveStream(context.Background(), a, 501, func(r io.Reader, _ io.Writer, _ uint32) error { _, err := io.Copy(io.Discard, r); return err },
			streamTimeouts{idle: time.Second, operation: 30 * time.Millisecond, cleanup: time.Second})
	}()
	if err := WriteFrame(b, []byte(`{"id":"c","command":"Connect"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var timeout net.Error
		if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &timeout) && timeout.Timeout()) {
			t.Fatalf("operation did not time out: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("operation deadline did not close host")
	}
}

func TestCloseInterruptsPendingExchangeAndServerOperation(t *testing.T) {
	a, b := net.Pipe()
	started := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveStream(context.Background(), a, 501, func(r io.Reader, _ io.Writer, _ uint32) error {
			s := bufio.NewScanner(r)
			if s.Scan() {
				close(started)
			}
			_, err := io.Copy(io.Discard, r)
			return err
		}, streamTimeouts{idle: time.Second, operation: time.Second, cleanup: time.Second})
	}()
	c := &Client{conn: b, timeout: time.Second}
	clientDone := make(chan error, 1)
	go func() { _, err := c.Exchange([]byte(`{"id":"c","command":"Connect"}`)); clientDone <- err }()
	<-started
	_ = c.Close()
	select {
	case err := <-clientDone:
		if err == nil {
			t.Fatal("closed exchange succeeded")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client Close waited for operation deadline")
	}
	select {
	case <-serverDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server did not detect EOF during operation")
	}
}

func TestBlockedReplyCannotExtendOperationDeadline(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		done <- serveStream(context.Background(), a, 501, func(r io.Reader, w io.Writer, _ uint32) error {
			s := bufio.NewScanner(r)
			if !s.Scan() {
				return s.Err()
			}
			time.Sleep(20 * time.Millisecond)
			_, err := w.Write([]byte("{\"id\":\"c\",\"ok\":true}\n"))
			return err
		}, streamTimeouts{idle: time.Second, operation: 50 * time.Millisecond, cleanup: time.Second})
	}()
	if err := WriteFrame(b, []byte(`{"id":"c","command":"Connect"}`)); err != nil {
		t.Fatal(err)
	}
	// Deliberately never read the reply: the host's write and input stream
	// must close under the original operation budget, without a fresh budget.
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked reply succeeded")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reply wait escaped operation deadline")
	}
}

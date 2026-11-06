package mactunipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Accepting a reused PID or a same-user unrelated process would let it submit
// credentials and privileged network operations for a different authorization.
func TestAuthenticateRequiresKernelIdentity(t *testing.T) {
	want := Identity{UID: 501, PID: 42, StartSeconds: 123, StartMicroseconds: 456}
	for _, tt := range []struct {
		name       string
		peer, live Identity
		ok         bool
	}{
		{"authorized", want, want, true},
		{"wrong uid", Identity{502, 42, 123, 456}, want, false},
		{"wrong pid", Identity{501, 43, 123, 456}, want, false},
		{"reused pid", want, Identity{501, 42, 124, 456}, false},
		{"changed subsecond birth", want, Identity{501, 42, 123, 457}, false},
		{"dead", want, Identity{}, false},
		{"root is not broker", Identity{0, 42, 123, 456}, want, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Authenticate(want, tt.peer, tt.live); (got == nil) != tt.ok {
				t.Fatalf("Authenticate = %v", got)
			}
		})
	}
}

func TestFrameBoundsAndTruncation(t *testing.T) {
	for _, n := range []uint32{0, 65537, 0xffffffff} {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, n)
		if _, err := ReadFrame(&b); !errors.Is(err, ErrFrame) {
			t.Fatalf("size %d: %v", n, err)
		}
	}
	var truncated bytes.Buffer
	_ = binary.Write(&truncated, binary.LittleEndian, uint32(3))
	truncated.WriteByte('x')
	if _, err := ReadFrame(&truncated); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	for _, n := range []int{1, 65536} {
		var b bytes.Buffer
		data := bytes.Repeat([]byte{'x'}, n)
		if err := WriteFrame(&b, data); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFrame(&b)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("round trip %d: %v", n, err)
		}
	}
	if err := WriteFrame(io.Discard, make([]byte, 65537)); !errors.Is(err, ErrFrame) {
		t.Fatal(err)
	}
}

func TestIntentRejectsCorruptOrUnknownMetadata(t *testing.T) {
	for _, data := range []string{``, `{}`, `{"version":1,"ownerUid":501,"active":true,"recoveryRequired":false,"secret":"x"}`, `{"version":1,"ownerUid":0,"active":true,"recoveryRequired":false}`, `{"version":1,"ownerUid":501,"active":true,"active":false,"recoveryRequired":false}`, `{"version":2,"ownerUid":501,"active":true,"recoveryRequired":false}`} {
		if _, err := DecodeIntent([]byte(data)); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
	i, err := DecodeIntent([]byte(`{"version":1,"ownerUid":501,"active":true,"recoveryRequired":false}`))
	if err != nil || !i.Active || i.OwnerUID != 501 {
		t.Fatalf("%+v %v", i, err)
	}
}

func TestUnsafeRootMetadata(t *testing.T) {
	for _, tt := range []struct {
		name  string
		uid   uint32
		mode  uint32
		links uint64
		kind  string
		ok    bool
	}{
		{"root file", 0, 0600, 1, "file", true},
		{"user file", 501, 0600, 1, "file", false},
		{"group writable", 0, 0620, 1, "file", false},
		{"world writable", 0, 0602, 1, "file", false},
		{"hard link", 0, 0600, 2, "file", false},
		{"symlink", 0, 0600, 1, "symlink", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if (validateRootMetadata(tt.uid, tt.mode, tt.links, tt.kind) == nil) != tt.ok {
				t.Fatal("unsafe metadata accepted or safe metadata rejected")
			}
		})
	}
}

func TestAuthorizationCancellationAndDenial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := authorizeAndDial(ctx, func(context.Context) error { called = true; return nil }, func(context.Context) (net.Conn, error) { called = true; return nil, nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("%v called=%v", err, called)
	}
	_, err = authorizeAndDial(context.Background(), func(context.Context) error { return ErrAuthorization }, func(context.Context) (net.Conn, error) { t.Fatal("dial after denial"); return nil, nil })
	if !errors.Is(err, ErrAuthorization) {
		t.Fatal(err)
	}
}

func TestLateApprovalNeverDials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := authorizeAndDial(ctx, func(context.Context) error { cancel(); return nil }, func(context.Context) (net.Conn, error) { t.Fatal("late approval dialed"); return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestShellArgumentsCannotBecomeCommands(t *testing.T) {
	for _, path := range []string{"/Applications/Blue Grass.app/Contents/MacOS/BlueGrass.Tunnel", "/tmp/a'$(touch BAD);`bad`\\b"} {
		q, err := shellQuote(path)
		if err != nil || !strings.HasPrefix(q, "'") || !strings.HasSuffix(q, "'") {
			t.Fatalf("%s %v", q, err)
		}
	}
	if _, err := shellQuote("/tmp/a\x00b"); err == nil {
		t.Fatal("NUL accepted")
	}
	if _, err := InstallationCommand("/tmp/worker", "$(bad)", Identity{501, 42, 123, 456}); err == nil {
		t.Fatal("invalid hash accepted")
	}
}

func TestSessionEOFClosesHostStream(t *testing.T) {
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- ServeStream(context.Background(), server, 501, func(r io.Reader, w io.Writer, uid uint32) error {
			if uid != 501 {
				return errors.New("wrong owner")
			}
			_, err := io.Copy(w, r)
			return err
		})
	}()
	if err := WriteFrame(client, []byte(`{"id":"h","command":"Hello"}`)); err != nil {
		t.Fatal(err)
	}
	reply, err := ReadFrame(client)
	if err != nil || string(reply) != `{"id":"h","command":"Hello"}` {
		t.Fatalf("%s %v", reply, err)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("host retained after broker EOF")
	}
}

func TestSessionRejectsInvalidJSONAndStopsOnCancel(t *testing.T) {
	for _, payload := range []string{`not json`, `{"id":1,"id":2}`, `[]`} {
		a, b := net.Pipe()
		done := make(chan error, 1)
		go func() {
			done <- ServeStream(context.Background(), a, 501, func(r io.Reader, _ io.Writer, _ uint32) error { _, err := io.Copy(io.Discard, r); return err })
		}()
		_ = WriteFrame(b, []byte(payload))
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("invalid JSON accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("frame did not close host")
		}
		_ = b.Close()
	}
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeStream(ctx, a, 501, func(r io.Reader, _ io.Writer, _ uint32) error { _, err := io.Copy(io.Discard, r); return err })
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel retained host")
	}
}

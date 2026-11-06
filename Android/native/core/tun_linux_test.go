//go:build linux && !android

package bridge

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"testing"
	"time"
)

func TestMobileTUNDuplicateCloseKeepsOriginalAndUnblocksRead(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pair[0])
	defer unix.Close(pair[1])
	device, err := openMobileTun(pair[0])
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := device.ReadPacket(make([]byte, 1500)); done <- err }()
	if err = device.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatal("unexpected canceled read", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed duplicate leaked blocked read")
	}
	if _, err = unix.FcntlInt(uintptr(pair[0]), unix.F_GETFD, 0); err != nil {
		t.Fatal("native close consumed Kotlin descriptor", err)
	}
	if _, err = unix.Write(pair[1], []byte("original")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 64)
	if n, err := unix.Read(pair[0], b); err != nil || string(b[:n]) != "original" {
		t.Fatal("original descriptor no longer receives packets", err)
	}
}

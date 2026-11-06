package serviceipc

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestFrameRoundTripAndBoundary(t *testing.T) {
	for _, size := range []int{1, 127, MaxFrame} {
		data := bytes.Repeat([]byte{'x'}, size)
		var wire bytes.Buffer
		if err := WriteFrame(&wire, data); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFrame(&wire)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("round trip %d: %v", size, err)
		}
	}
}
func TestFrameRejectsBeforeAllocatingBody(t *testing.T) {
	for _, size := range []uint32{0, MaxFrame + 1, 0xffffffff} {
		var header [4]byte
		binary.LittleEndian.PutUint32(header[:], size)
		if _, err := ReadFrame(bytes.NewReader(header[:])); err != ErrFrame {
			t.Fatalf("length %d: %v", size, err)
		}
	}
	if err := WriteFrame(io.Discard, make([]byte, MaxFrame+1)); err != ErrFrame {
		t.Fatal(err)
	}
}
func TestFrameRejectsTruncationAndHandlesShortWrites(t *testing.T) {
	for _, b := range [][]byte{{1}, {2, 0, 0, 0, 'a'}} {
		if _, err := ReadFrame(bytes.NewReader(b)); err == nil {
			t.Fatal("accepted truncation")
		}
	}
	if err := WriteFrame(shortWriter{}, []byte("hello")); err == nil {
		t.Fatal("accepted short write")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestOnlyCurrentProcessLeaseCanChangeSession(t *testing.T) {
	var lease Lease
	one := Identity{SID: "S-1-5-21-1", Session: 1, PID: 12}
	other := Identity{SID: "S-1-5-21-2", Session: 2, PID: 13}
	if !lease.Acquire(one) || lease.Acquire(other) || lease.Acquire(one) {
		t.Fatal("concurrent owner accepted")
	}
	lease.Release(other)
	if lease.Acquire(other) {
		t.Fatal("foreign release removed owner")
	}
	lease.Release(one)
	if !lease.Acquire(other) {
		t.Fatal("released owner remained locked")
	}
}

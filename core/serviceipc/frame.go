// Package serviceipc defines the bounded local service wire format. Credentials
// only cross authenticated private pipes; no control TCP socket is used.
package serviceipc

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

const MaxFrame = 64 * 1024

var ErrFrame = errors.New("INVALID_SERVICE_FRAME")

func ReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n == 0 || n > MaxFrame {
		return nil, ErrFrame
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
func WriteFrame(w io.Writer, data []byte) error {
	if len(data) == 0 || len(data) > MaxFrame {
		return ErrFrame
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	for _, part := range [][]byte{header[:], data} {
		n, err := w.Write(part)
		if err != nil {
			return err
		}
		if n != len(part) {
			return io.ErrShortWrite
		}
	}
	return nil
}

type Identity struct {
	SID          string
	Session, PID uint32
}
type Lease struct {
	mu    sync.Mutex
	owner *Identity
}

func (l *Lease) Acquire(caller Identity) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner != nil || caller.SID == "" || caller.PID == 0 || caller.Session == 0 {
		return false
	}
	l.owner = &caller
	return true
}
func (l *Lease) Release(caller Identity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner != nil && *l.owner == caller {
		l.owner = nil
	}
}

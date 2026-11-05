// Package mactunipc implements the private, administrator-authorized Darwin
// tunnel transport. It deliberately has no dependency on nativehost.
package mactunipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"

	"bluegrass/native/core/control"
)

const (
	RuntimeDirectory = "/var/run/bluegrass-tun"
	StateDirectory   = "/Library/Application Support/BlueGrass/Tunnel"
	IntentPath       = "/Library/Application Support/BlueGrass/tun-intent.json"
	SocketPath       = RuntimeDirectory + "/worker.sock"
	MaxFrame         = 64 * 1024
	ProtocolVersion  = 1
)

// ExpectedWorkerSHA256 is set only by -ldflags -X, after signing the worker.
// An empty or malformed digest disables authorization; there is no fallback.
var ExpectedWorkerSHA256 string

var (
	ErrAuthorization        = errors.New("AUTHORIZATION_CANCELLED")
	ErrAuthorizationTimeout = errors.New("AUTHORIZATION_TIMEOUT")
	ErrHelperInvalid        = errors.New("PRIVILEGED_HELPER_INVALID")
	ErrBusy                 = errors.New("SERVICE_BUSY")
	ErrUnavailable          = errors.New("SERVICE_UNAVAILABLE")
	ErrRecovery             = errors.New("RECOVERY_REQUIRED")
	ErrFrame                = errors.New("INVALID_TUN_FRAME")
)

type Identity struct {
	UID, PID                        uint32
	StartSeconds, StartMicroseconds int64
}

func (i Identity) valid() bool {
	return i.UID != 0 && i.PID > 1 && i.PID <= 0x7fffffff && i.StartSeconds > 0 && i.StartMicroseconds >= 0 && i.StartMicroseconds < 1000000
}

// peer comes from LOCAL_PEERCRED/LOCAL_PEERPID, live from kern.proc.pid.
// Never accept identity fields supplied by a JSON request.
func Authenticate(expected, peer, live Identity) error {
	if !expected.valid() || peer != expected || live != expected {
		return ErrUnavailable
	}
	return nil
}

// Intent is non-secret discovery metadata shared with the network manager.
// The manager owns atomic persistence/removal; IPC never clears protection.
type Intent struct {
	Version          int    `json:"version"`
	OwnerUID         uint32 `json:"ownerUid"`
	Active           bool   `json:"active"`
	RecoveryRequired bool   `json:"recoveryRequired"`
}

func DecodeIntent(data []byte) (Intent, error) {
	var i Intent
	if len(data) > 1024 || control.ValidateJSON(data) != nil {
		return i, ErrRecovery
	}
	// Require every field, including false booleans; omitted state is uncertain.
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 4 {
		return i, ErrRecovery
	}
	for _, k := range []string{"version", "ownerUid", "active", "recoveryRequired"} {
		if len(fields[k]) == 0 || string(fields[k]) == "null" {
			return i, ErrRecovery
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&i) != nil || i.Version != 1 || i.OwnerUID == 0 {
		return Intent{}, ErrRecovery
	}
	return i, nil
}

func validateRootMetadata(uid, mode uint32, links uint64, kind string) error {
	if uid != 0 || mode&0022 != 0 || kind != "file" || links != 1 {
		return ErrHelperInvalid
	}
	return nil
}

func validHash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == 32 && hash == strings.ToLower(hash)
}

// WorkerInfo is safe to print without reading configuration or touching disk.
func WorkerInfo() []byte {
	b, _ := json.Marshal(struct {
		ExpectedWorkerSHA256 string `json:"expectedWorkerSHA256"`
		ProtocolVersion      int    `json:"protocolVersion"`
	}{ExpectedWorkerSHA256, ProtocolVersion})
	return append(b, '\n')
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n == 0 || n > MaxFrame {
		return nil, ErrFrame
	}
	b := make([]byte, int(n))
	_, err := io.ReadFull(r, b)
	return b, err
}

func WriteFrame(w io.Writer, data []byte) error {
	if len(data) == 0 || len(data) > MaxFrame {
		return ErrFrame
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	for _, b := range [][]byte{header[:], data} {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n != len(b) {
			return io.ErrShortWrite
		}
	}
	return nil
}

func authorizeAndDial(ctx context.Context, authorize func(context.Context) error, dial func(context.Context) (net.Conn, error)) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := authorize(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := dial(ctx)
	if err == nil && ctx.Err() != nil {
		if c != nil {
			_ = c.Close()
		}
		return nil, ctx.Err()
	}
	return c, err
}

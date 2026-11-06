//go:build darwin

package nativehost

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"

	"bluegrass/native/core/mactun"
	"bluegrass/native/core/mactunipc"
)

// RunPrivilegedDarwinTunnel runs only after mactunipc authenticates the broker's
// kernel UID/PID/birth identity. The manager requires root and owns the global
// network lease. Stream closure retains protection for explicit recovery.
func RunPrivilegedDarwinTunnel(input io.Reader, output io.Writer, ownerUID uint32) error {
	network, err := mactun.New(ownerUID)
	if err != nil {
		return rejectDarwinTunnelStartup(input, output, err)
	}
	return NewTunnel(network).run(input, output)
}

// A refused root transaction still answers the real Hello request with a fixed
// code. Never expose raw filesystem, command or profile details to the GUI.
func rejectDarwinTunnelStartup(input io.Reader, output io.Writer, cause error) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), mactunipc.MaxFrame+2)
	if !scanner.Scan() {
		return errors.Join(mactun.ErrRecovery, scanner.Err())
	}
	r, err := Decode(scanner.Bytes())
	if err != nil || r.Command != "Hello" || r.ID == "" || len(r.ID) > 128 || len(scanner.Bytes()) > mactunipc.MaxFrame {
		return mactun.ErrRecovery
	}
	code := logicalSessionError(cause)
	data, _ := json.Marshal(Response{ID: r.ID, OK: false, Error: code})
	_, err = output.Write(append(data, '\n'))
	return errors.Join(errors.New(code), err)
}

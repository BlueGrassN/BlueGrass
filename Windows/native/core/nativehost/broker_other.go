//go:build !windows && !darwin

package nativehost

import (
	"errors"
	"io"
)

func RunTunnelBroker(io.Reader, io.Writer) error { return errors.New("WINDOWS_REQUIRED") }

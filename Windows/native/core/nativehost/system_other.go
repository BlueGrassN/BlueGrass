//go:build !windows && !darwin

package nativehost

import (
	"bluegrass/native/core/winproxy"
	"io"
)

func RunSystemProxy(input io.Reader, output io.Writer) error { return winproxy.ErrUnsupported }

func RepairCurrentUserProxy() (bool, error) { return false, winproxy.ErrUnsupported }

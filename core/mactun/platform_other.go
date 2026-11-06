//go:build !darwin

package mactun

import "errors"

func New(ownerUID uint32) (*Tunnel, error) { return nil, errors.New("MACOS_ROOT_TUN_REQUIRED") }

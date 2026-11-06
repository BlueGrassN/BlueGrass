//go:build darwin

// BlueGrass.Tunnel is an on-demand administrator-authorized worker. It accepts
// only fixed numeric broker identity arguments, never commands or profiles.
package main

import (
	"bluegrass/native/core/mactunipc"
	"bluegrass/native/core/nativehost"
	"os"
)

func main() {
	owner, err := mactunipc.ParseOwner(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	if mactunipc.Serve(owner, nativehost.RunPrivilegedDarwinTunnel) != nil {
		os.Exit(1)
	}
}

package main

import (
	"bluegrass/native/core/nativehost"
	"os"
)

func main() {
	run := nativehost.Run
	if len(os.Args) == 2 && os.Args[1] == "--system-proxy" {
		run = nativehost.RunSystemProxy
	} else if len(os.Args) == 2 && os.Args[1] == "--tun" {
		run = nativehost.RunTunnelBroker
	} else if len(os.Args) != 1 {
		os.Exit(2)
	}
	if run(os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
}

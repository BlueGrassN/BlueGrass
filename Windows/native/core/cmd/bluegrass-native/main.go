package main

import (
	"bluegrass/native/core/mactunipc"
	"bluegrass/native/core/nativehost"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--tun-worker-info" {
		if _, err := os.Stdout.Write(mactunipc.WorkerInfo()); err != nil {
			os.Exit(1)
		}
		return
	}
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

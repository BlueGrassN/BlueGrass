//go:build windows

package main

import (
	"bluegrass/native/core/cmdupdate"
	"context"
	"os"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	var err error
	switch {
	case len(os.Args) == 2 && os.Args[1] == "--rpc":
		err = cmdupdate.Run(ctx, os.Stdin, os.Stdout)
	case len(os.Args) == 4 && os.Args[1] == "--apply":
		err = cmdupdate.Apply(ctx, os.Args[2], os.Args[3])
	default:
		os.Exit(2)
	}
	if err != nil {
		os.Exit(1)
	}
}

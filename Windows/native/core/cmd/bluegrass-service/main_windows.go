//go:build windows

package main

import (
	"bluegrass/native/core/winservice"
	"encoding/json"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = winservice.Run()
	case "install":
		err = winservice.Install()
	case "start":
		err = winservice.Start()
	case "maintenance":
		err = winservice.Maintenance()
	case "uninstall":
		err = winservice.Uninstall()
	case "status":
		var value map[string]any
		value, err = winservice.Status()
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(value)
		}
	default:
		os.Exit(2)
	}
	if err != nil {
		_, _ = os.Stderr.WriteString("BlueGrass service operation failed.\n")
		os.Exit(1)
	}
}

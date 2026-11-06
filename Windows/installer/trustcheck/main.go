// BlueGrass.SetupTrust is an installer-only Authenticode gate. It is not a
// service controller or a repair tool, and must be signed for release packaging.
package main

import (
	"bluegrass/native/core/update"
	"os"
)

func main() {
	if len(os.Args) != 3 || update.VerifyPublisher(os.Args[1], os.Args[2]) != nil {
		os.Exit(1)
	}
}

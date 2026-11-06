//go:build windows

package main

import (
	"encoding/json"
	"os"

	"bluegrass/native/core/nativehost"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
	"bluegrass/native/core/winservice"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(2)
	}
	// The executable manifest requests elevation. Stop and remove only known
	// product objects; never uninstall the shared Wintun driver.
	if winservice.Maintenance() != nil {
		fail("SERVICE_STOP_FAILED")
	}
	if tunengine.RepairAdapter() != nil {
		fail("TUN_REPAIR_FAILED")
	}
	if winservice.ClearOwnedCache() != nil {
		fail("CACHE_REPAIR_FAILED")
	}
	if winprotect.Repair() != nil {
		fail("PROTECTION_REPAIR_FAILED")
	}
	preserved, err := nativehost.RepairCurrentUserProxy()
	if err != nil {
		fail("USER_PROXY_REPAIR_FAILED")
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"repaired": true, "proxySettingsPreserved": preserved})
}
func fail(code string) {
	_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"repaired": false, "error": code})
	os.Exit(1)
}

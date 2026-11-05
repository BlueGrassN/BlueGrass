package winprotect

import (
	"path/filepath"
	"testing"
)

func TestUpdateHelperPermissionIsFixedImageTCP443Only(t *testing.T) {
	m, _, fw := testManager(t)
	arm(t, m)
	if err := m.AllowUpdateHelper(); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(filepath.Dir(m.policy.UI), "BlueGrass.Update.exe")
	for _, app := range []string{helper, "BlueGrass.Native.exe", "svchost.exe", "other.exe"} {
		for _, proto := range []uint8{6, 17} {
			for _, port := range []uint16{53, 80, 443, 8443} {
				want := app == helper && proto == 6 && port == 443
				if permits(fw.rules, packet{addr: "8.8.8.8", remote: port, protocol: proto, app: app}) != want {
					t.Fatalf("app=%s proto=%d port=%d", app, proto, port)
				}
			}
		}
	}
	if permits(fw.rules, packet{addr: "198.18.0.1", remote: 443, protocol: 6, app: helper}) {
		t.Fatal("helper bypassed virtual address exclusion")
	}
}

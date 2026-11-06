//go:build darwin

package tunengine

import (
	"net/netip"
	"testing"

	tun "github.com/xtls/xray-core/proxy/tun"
)

func TestDarwinNativeDNSIsRemotePeer(t *testing.T) {
	opts := nativeOptions(netip.MustParsePrefix("172.31.255.1/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126"))
	if opts.Name != "utun233" || len(opts.DNS) != 1 || opts.DNS[0] != "172.31.255.1" || len(opts.AutoSystemRoutingTable) != 0 {
		t.Fatal("Darwin host/DNS or route ownership reversed", opts)
	}
}

func TestDarwinNativeRejectsWrongIdentityBeforeOpening(t *testing.T) {
	for _, opts := range []*tun.Config{nil, {Name: "utun0"}, {Name: "BlueGrass"}} {
		d, index, err := openNativeTun(opts)
		if err == nil || err.Error() != "TUN_ADAPTER_IDENTITY_MISMATCH" || d != nil || index != 0 {
			t.Fatal("foreign adapter accepted")
		}
	}
}

func TestDarwinNativeRejectsInheritedXrayFD(t *testing.T) {
	t.Setenv("XRAY_TUN_FD", "42")
	t.Setenv("xray.tun.fd", "42")
	d, index, err := openNativeTun(nativeOptions(netip.MustParsePrefix("172.31.255.1/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126")))
	if err == nil || err.Error() != "TUN_INHERITED_FD_FORBIDDEN" || d != nil || index != 0 {
		t.Fatal("inherited adapter accepted")
	}
}

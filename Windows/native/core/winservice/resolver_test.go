package winservice

import (
	"net/netip"
	"testing"

	"bluegrass/native/core/entrydns"
)

func TestLiteralEntryCreatesNoDNSPermissions(t *testing.T) {
	permissions, err := entryPermissions("203.0.113.9", entrydns.Policy{}, nil)
	if err != nil || len(permissions) != 0 {
		t.Fatal("literal entry invented DNS permissions")
	}
}
func TestUsableDNSDeduplicatesAndRejectsUnenforceableEndpoints(t *testing.T) {
	var input []netip.AddrPort
	for _, s := range []string{"192.168.1.1:53", "192.168.1.1:53", "[fe80::1%12]:53", "[fe80::1]:53", "169.254.1.1:53", "127.0.0.1:53", "[::]:53", "224.0.0.1:53", "[::ffff:192.168.1.1]:53", "1.1.1.1:0"} {
		input = append(input, netip.MustParseAddrPort(s))
	}
	output := usableDNS(input)
	if len(output) != 1 || output[0].String() != "192.168.1.1:53" {
		t.Fatalf("unsafe DNS selection: %v", output)
	}
}

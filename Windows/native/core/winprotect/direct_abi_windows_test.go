//go:build windows && (amd64 || arm64)

package winprotect

import (
	"net/netip"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

// Encode only into process memory; never create a WFP session or call its API.
func TestDirectEncodingIsEphemeralExactOutboundServiceAccess(t *testing.T) {
	m, _, f := testManager(t)
	d := startDirectTunnel(t, m)
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	for _, network := range []string{"tcp", "udp"} {
		if err := d.AllowDirect(network, ep); err != nil {
			t.Fatal(err)
		}
	}
	appBytes := []byte{1, 2}
	app := &byteBlob{Size: uint32(len(appBytes)), Data: &appBytes[0]}
	protocols := map[uint8]bool{}
	for _, r := range f.rules {
		if r.Kind != "direct" {
			continue
		}
		if protocols[r.Protocol] || (r.Protocol != 6 && r.Protocol != 17) || r.App != "service.exe" {
			t.Fatal("duplicate or malformed direct rule", r)
		}
		protocols[r.Protocol] = true
		if _, err := encodeFilter(r, nil); err == nil {
			t.Fatal("missing service identity broadened direct access")
		}
		e, err := encodeFilter(r, app)
		if err != nil {
			t.Fatal(err)
		}
		if e.filter.Flags != 0 || e.filter.Layer != aleConnect4 || e.filter.Action.Type != 0x1002 || e.filter.NumConditions != 4 {
			t.Fatal("direct flags, layer or conditions broadened")
		}
		seen := map[windows.GUID]wfpCondition{}
		for _, c := range e.conditions {
			if _, duplicate := seen[c.Field]; duplicate || c.Match != 0 {
				t.Fatal("direct conditions must be distinct exact matches")
			}
			seen[c.Field] = c
		}
		if seen[fieldApp].Value.Type != 12 || seen[fieldProtocol].Value.Type != 1 || seen[fieldProtocol].Value.Value != uintptr(r.Protocol) || seen[fieldRemotePort].Value.Type != 2 || seen[fieldRemotePort].Value.Value != 50001 || seen[fieldRemote].Value.Type != 256 {
			t.Fatal("service, protocol, IPv4 or port condition missing")
		}
		maskOK, appOK := false, false
		for _, keep := range e.keep {
			switch v := keep.(type) {
			case *v4Mask:
				maskOK = v.Addr == 0x08080808 && v.Mask == 0xffffffff
			case *byteBlob:
				appOK = v == app
			}
		}
		if !maskOK || !appOK {
			t.Fatal("direct host or application identity broadened")
		}
		runtime.KeepAlive(e)
	}
	if len(protocols) != 2 {
		t.Fatal("missing distinct TCP/UDP direct filters")
	}
}

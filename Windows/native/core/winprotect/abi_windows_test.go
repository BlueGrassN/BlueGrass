//go:build windows && (amd64 || arm64)

package winprotect

import (
	"net/netip"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These tests allocate/inspect C ABI data only. They never open a WFP session.
func TestWindowsSDKABI(t *testing.T) {
	var f wfpFilter
	var c wfpCondition
	var e wfpEnumTemplate
	for _, v := range []struct {
		name      string
		got, want uintptr
	}{
		{"filter size", unsafe.Sizeof(f), 200}, {"filter condition pointer", unsafe.Offsetof(f.Conditions), 120},
		{"context union", unsafe.Offsetof(f.Context), 152}, {"filter id", unsafe.Offsetof(f.ID), 176},
		{"effective weight", unsafe.Offsetof(f.EffectiveWeight), 184}, {"condition size", unsafe.Sizeof(c), 40},
		{"condition value", unsafe.Offsetof(c.Value), 24}, {"enum size", unsafe.Sizeof(e), 72},
		{"provider size", unsafe.Sizeof(wfpProvider{}), 64}, {"sublayer size", unsafe.Sizeof(wfpSublayer{}), 72},
		{"session size", unsafe.Sizeof(wfpSession{}), 72},
		{"layer size", unsafe.Sizeof(wfpLayer{}), 72},
	} {
		if v.got != v.want {
			t.Errorf("%s=%d want %d", v.name, v.got, v.want)
		}
	}
}

func TestEncodingPreservesEndpointAndPortExclusions(t *testing.T) {
	r := rule{Kind: "lan", Persistent: true, Permit: true, Weight: 100, Remote: netip.MustParsePrefix("192.168.5.0/24"), Protocol: 17, LUID: 0x123456789, NoDNS: true}
	e, err := encodeFilter(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.filter.Flags != 1 || e.filter.Layer != aleConnect4 || e.filter.Action.Type != 0x1002 || e.filter.Weight.Type != 4 || e.weight != 100 {
		t.Fatal("incorrect native filter")
	}
	seen := map[windows.GUID]wfpCondition{}
	for _, c := range e.conditions {
		if _, ok := seen[c.Field]; ok {
			t.Fatal("duplicate condition field could be ORed")
		}
		seen[c.Field] = c
	}
	if c := seen[fieldProtocol]; c.Value.Type != 1 || c.Value.Value != 17 {
		t.Fatal("protocol encoding")
	}
	for _, key := range []windows.GUID{fieldRemotePort, fieldLocalPort} {
		if c := seen[key]; c.Match != 10 || c.Value.Type != 2 || c.Value.Value != 53 {
			t.Fatal("DNS exclusion lost")
		}
	}
	// Inspect GC roots, not uintptr-to-pointer conversions (also race/checkptr safe).
	maskOK, luidOK := false, false
	for _, keep := range e.keep {
		switch v := keep.(type) {
		case *v4Mask:
			maskOK = v.Addr == 0xc0a80500 && v.Mask == 0xffffff00
		case *uint64:
			luidOK = *v == 0x123456789
		}
	}
	if !maskOK || !luidOK {
		t.Fatal("address byte order or full LUID lost")
	}
	runtime.GC()
	runtime.KeepAlive(e)
	if _, err := encodeFilter(rule{App: "missing.exe"}, nil); err == nil {
		t.Fatal("broadened a missing application identity")
	}
}

func TestEncodingBootFiltersAndEphemeralTunnel(t *testing.T) {
	for _, r := range compilePolicy(policy{Tunnel: 42, Virtual: defaultVirtualPrefixes()}) {
		e, err := encodeFilter(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Boot {
			if e.filter.Flags != 2 || e.filter.NumConditions != 0 || e.filter.Action.Type != 0x1001 {
				t.Fatal("boot filter unsafe")
			}
			if e.filter.Layer != packetIn4 && e.filter.Layer != packetIn6 && e.filter.Layer != packetOut4 && e.filter.Layer != packetOut6 {
				t.Fatal("boot filter on wrong layer")
			}
		} else if r.Kind == "tunnel" {
			if e.filter.Flags != 0 || e.filter.NumConditions != 1 || e.conditions[0].Field != fieldInterface {
				t.Fatal("tunnel permit persisted or broadened")
			}
		} else if e.filter.Flags != 1 {
			t.Fatal("runtime protection not persistent")
		}
	}
}

func TestEncodingRelayIsEphemeralAndExact(t *testing.T) {
	appBytes := []byte{1, 2}
	app := &byteBlob{Size: uint32(len(appBytes)), Data: &appBytes[0]}
	rules := compilePolicy(policy{Service: "service.exe", Entry: testEntry, Tunnel: 42,
		EntryPolicy: EntryPolicy{TCP: true, AuthenticatedSOCKSRelay: true},
		UDPRelays:   []netip.AddrPort{netip.MustParseAddrPort("8.8.8.8:50001")}})
	found := false
	for _, r := range rules {
		if r.Kind != "udp-relay" {
			continue
		}
		found = true
		e, err := encodeFilter(r, app)
		if err != nil {
			t.Fatal(err)
		}
		if e.filter.Flags != 0 || e.filter.Layer != aleConnect4 || e.filter.Action.Type != 0x1002 || e.filter.NumConditions != 4 {
			t.Fatal("relay flags/layer/conditions broadened")
		}
		seen := map[windows.GUID]wfpCondition{}
		for _, c := range e.conditions {
			if c.Match != 0 {
				t.Fatal("relay conditions must all be exact matches")
			}
			seen[c.Field] = c
		}
		if seen[fieldApp].Value.Type != 12 || seen[fieldProtocol].Value.Type != 1 || seen[fieldProtocol].Value.Value != 17 || seen[fieldRemotePort].Value.Type != 2 || seen[fieldRemotePort].Value.Value != 50001 || seen[fieldRemote].Value.Type != 256 {
			t.Fatal("relay service, UDP, IP or port condition missing")
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
			t.Fatal("relay host or executable identity was broadened")
		}
		runtime.KeepAlive(e)
	}
	if !found {
		t.Fatal("relay rule missing")
	}
}

func TestRepairOwnershipRequiresBothGUIDs(t *testing.T) {
	other := windows.GUID{Data1: 123}
	for _, tc := range []struct {
		f    *wfpFilter
		want bool
	}{
		{nil, false}, {&wfpFilter{Provider: &providerKey, Sublayer: sublayerKey}, true},
		{&wfpFilter{Provider: &other, Sublayer: sublayerKey}, false},
		{&wfpFilter{Provider: &providerKey, Sublayer: other}, false},
		{&wfpFilter{Sublayer: sublayerKey}, false},
	} {
		if ownsFilter(tc.f) != tc.want {
			t.Fatal("repair claimed an object outside its GUID pair")
		}
	}
}

func TestStateDescriptorHasOnlySystemAndAdmin(t *testing.T) {
	// Parses a descriptor in process memory; does not create or change a file.
	sd, err := windows.SecurityDescriptorFromString(stateSDDL)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		t.Fatal("state owner grants user WRITE_DAC")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 {
		t.Fatal("state DACL is not restricted")
	}
	for i := uint32(0); i < 2; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || (!sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
			t.Fatal("untrusted state trustee")
		}
	}
	runtime.KeepAlive(sd)
}

//go:build windows

package tunengine

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	tun "github.com/xtls/xray-core/proxy/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func TestNativeReuseRequiresCompleteUniqueIdentity(t *testing.T) {
	for _, guid := range []string{expectedAdapterGUID(), legacyAdapterGUID()} {
		good := nativeAdapterRecord{adapterIdentity: adapterIdentity{id: "own", guid: guid, name: InterfaceName, service: "Wintun", hardwareIDs: []string{"Wintun"}}, wintunName: "BlueGrass"}
		for _, tc := range []struct {
			name   string
			mutate func(*nativeAdapterRecord)
		}{
			{"renamed alias", func(d *nativeAdapterRecord) { d.name = "Other VPN" }},
			{"alias case", func(d *nativeAdapterRecord) { d.name = "bluegrass" }},
			{"wrong service", func(d *nativeAdapterRecord) { d.service = "Other" }},
			{"missing hardware", func(d *nativeAdapterRecord) { d.hardwareIDs = nil }},
			{"foreign GUID with stored name", func(d *nativeAdapterRecord) { d.guid = "{00000000-0000-0000-0000-000000000000}" }},
			{"wrong stored name", func(d *nativeAdapterRecord) { d.wintunName = "Other" }},
		} {
			t.Run(guid+"/"+tc.name, func(t *testing.T) {
				d := good
				tc.mutate(&d)
				if _, err := selectNativeAdapter([]nativeAdapterRecord{d}); err == nil {
					t.Fatal("incomplete ownership accepted")
				}
			})
		}
		good.wintunName = "bLuEgRaSs" // Wintun 0.14.1 uses _wcsicmp.
		selected, err := selectNativeAdapter([]nativeAdapterRecord{good})
		if err != nil || selected == nil || selected.guid != guid {
			t.Fatalf("valid identity rejected: %v", err)
		}
		other := good
		other.guid = expectedAdapterGUID()
		if guid == expectedAdapterGUID() {
			other.guid = legacyAdapterGUID()
		}
		if _, err := selectNativeAdapter([]nativeAdapterRecord{good, other}); err == nil {
			t.Fatal("mixed product identities accepted")
		}
	}
}

func nativeCleanupTestRoute(t *testing.T, luid winipcfg.LUID, prefix string, protocol winipcfg.RouteProtocol) winipcfg.MibIPforwardRow2 {
	t.Helper()
	route := winipcfg.MibIPforwardRow2{InterfaceLUID: luid, Protocol: protocol}
	if err := route.DestinationPrefix.SetPrefix(netip.MustParsePrefix(prefix)); err != nil {
		t.Fatal(err)
	}
	next := netip.IPv4Unspecified()
	if route.DestinationPrefix.Prefix().Addr().Is6() {
		next = netip.IPv6Unspecified()
	}
	if err := route.NextHop.SetAddr(next); err != nil {
		t.Fatal(err)
	}
	return route
}

func nativeCleanupTestDevice() *checkedNativeTun {
	return &checkedNativeTun{luid: 42, guid: expectedAdapterGUID(),
		ownedRoutes:    []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1"), netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1")},
		ownedAddresses: []netip.Addr{netip.MustParseAddr("172.31.255.1"), netip.MustParseAddr("fdfe:dcba:9876::1")},
	}
}

func TestNativeCleanupAllowsOnlyAutomaticLocalRoutes(t *testing.T) {
	for _, tc := range []struct {
		prefix    string
		protocol  winipcfg.RouteProtocol
		wantError bool
	}{
		{"fe80::/64", winipcfg.RouteProtocolLocal, false},
		{"ff00::/8", winipcfg.RouteProtocolLocal, false},
		{"169.254.0.0/16", winipcfg.RouteProtocolLocal, false},
		{"224.0.0.0/4", winipcfg.RouteProtocolLocal, false},
		{"255.255.255.255/32", winipcfg.RouteProtocolLocal, false},
		{"fe80::/64", winipcfg.RouteProtocolNetMgmt, true},
		{"0.0.0.0/1", winipcfg.RouteProtocolNetMgmt, true},
		{"128.0.0.0/1", winipcfg.RouteProtocolNetMgmt, true},
		{"::/1", winipcfg.RouteProtocolNetMgmt, true},
		{"8000::/1", winipcfg.RouteProtocolNetMgmt, true},
		{"172.31.255.0/30", winipcfg.RouteProtocolLocal, true},
		{"172.31.255.1/32", winipcfg.RouteProtocolLocal, true},
		{"fdfe:dcba:9876::/126", winipcfg.RouteProtocolLocal, true},
		{"10.0.0.0/8", winipcfg.RouteProtocolNetMgmt, true},
	} {
		t.Run(tc.prefix+"/"+strconv.Itoa(int(tc.protocol)), func(t *testing.T) {
			d := nativeCleanupTestDevice()
			state := nativeCleanupSnapshot{routes: []winipcfg.MibIPforwardRow2{nativeCleanupTestRoute(t, 42, tc.prefix, tc.protocol)}}
			err := d.verifyCleanup(state)
			if (err != nil) != tc.wantError {
				t.Fatalf("cleanup = %v, want error %v", err, tc.wantError)
			}
		})
	}
	d := nativeCleanupTestDevice()
	state := nativeCleanupSnapshot{routes: []winipcfg.MibIPforwardRow2{nativeCleanupTestRoute(t, 99, "0.0.0.0/0", winipcfg.RouteProtocolNetMgmt)}}
	if err := d.verifyCleanup(state); err != nil {
		t.Fatal("unrelated interface affected cleanup", err)
	}
	d.ownedRoutes = append(d.ownedRoutes, netip.MustParsePrefix("fe80::/64"))
	state.routes = []winipcfg.MibIPforwardRow2{nativeCleanupTestRoute(t, 42, "fe80::/64", winipcfg.RouteProtocolLocal)}
	if err := d.verifyCleanup(state); err == nil {
		t.Fatal("explicitly configured local route was ignored")
	}
}

func TestNativeCleanupRequiresAddressesAndDNSConfirmed(t *testing.T) {
	d := nativeCleanupTestDevice()
	for _, addr := range []string{"172.31.255.1", "fdfe:dcba:9876::1", "fe80::1"} {
		row := winipcfg.MibUnicastIPAddressRow{InterfaceLUID: 42}
		if err := row.Address.SetAddr(netip.MustParseAddr(addr)); err != nil {
			t.Fatal(err)
		}
		state := nativeCleanupSnapshot{addresses: []winipcfg.MibUnicastIPAddressRow{row}}
		if err := d.verifyCleanup(state); (err != nil) != (addr != "fe80::1") {
			t.Fatalf("address %s: %v", addr, err)
		}
	}
	for _, state := range []nativeCleanupSnapshot{
		{interfacePresent: true}, // Empty DNS without matching adapter is unknown.
		{interfacePresent: true, dnsEnumerated: true, dns: []netip.Addr{netip.MustParseAddr("172.31.255.1")}},
		{configuredDNS: []string{"172.31.255.1"}}, // PnP disappearance does not clear persisted DNS.
	} {
		if err := d.verifyCleanup(state); err == nil || !strings.Contains(err.Error(), "DNS") {
			t.Fatalf("unconfirmed DNS accepted: %v", err)
		}
	}
	if err := d.verifyCleanup(nativeCleanupSnapshot{interfacePresent: true, dnsEnumerated: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckedNativeCloseWaitsForDNSRemoval(t *testing.T) {
	d := nativeCleanupTestDevice()
	raw := &fakeNativeDevice{}
	d.Tun = raw
	d.checkIdentity = func(bool) error { return nil }
	reads := 0
	d.snapshot = func(winipcfg.LUID, string) (nativeCleanupSnapshot, error) {
		reads++
		if reads == 1 {
			return nativeCleanupSnapshot{configuredDNS: []string{"172.31.255.2"}}, nil
		}
		return nativeCleanupSnapshot{}, nil
	}
	if err := d.Close(); err != nil {
		t.Fatal("pending DNS teardown was treated as permanent cleanup failure", err)
	}
	if reads < 2 || raw.closes != 1 {
		t.Fatal("cleanup must recheck state without repeating native mutations")
	}
}

func TestCheckedNativeClosePreservesErrorsAndChecksOwnershipFirst(t *testing.T) {
	for _, identityOK := range []bool{false, true} {
		upstreamErr := errors.New("upstream close")
		inspectionErr := errors.New("inspection failed")
		raw := &fakeNativeDevice{closeErr: upstreamErr}
		d := nativeCleanupTestDevice()
		d.Tun = raw
		d.checkIdentity = func(bool) error {
			if !identityOK {
				return errors.New("ownership changed")
			}
			return nil
		}
		d.snapshot = func(winipcfg.LUID, string) (nativeCleanupSnapshot, error) {
			return nativeCleanupSnapshot{}, inspectionErr
		}
		err := d.Close()
		if err == nil {
			t.Fatal("cleanup uncertainty hidden")
		}
		if identityOK && (!errors.Is(err, upstreamErr) || !errors.Is(err, inspectionErr)) {
			t.Fatal("upstream or inspection error was discarded", err)
		}
		if d.Close() == nil {
			t.Fatal("cleanup uncertainty lost on second Close")
		}
		want := 0
		if identityOK {
			want = 1
		}
		if raw.closes != want {
			t.Fatalf("unsafe or repeated native Close: %d", raw.closes)
		}
	}
}

func TestCheckedNativeCloseUsesCleanupSnapshot(t *testing.T) {
	for _, productRouteRemains := range []bool{false, true} {
		d := nativeCleanupTestDevice()
		raw := &fakeNativeDevice{}
		d.Tun = raw
		d.checkIdentity = func(bool) error { return nil }
		prefix := "fe80::/64"
		if productRouteRemains {
			prefix = "172.31.255.0/30"
		}
		d.snapshot = func(winipcfg.LUID, string) (nativeCleanupSnapshot, error) {
			return nativeCleanupSnapshot{routes: []winipcfg.MibIPforwardRow2{nativeCleanupTestRoute(t, 42, prefix, winipcfg.RouteProtocolLocal)}}, nil
		}
		if err := d.Close(); (err != nil) != productRouteRemains {
			t.Fatalf("Close = %v, product route remains = %v", err, productRouteRemains)
		}
		_ = d.Close()
		if raw.closes != 1 {
			t.Fatal("native close was repeated")
		}
	}
}

func TestCheckedNativeStartChecksOwnership(t *testing.T) {
	raw := &fakeNativeDevice{}
	d := nativeCleanupTestDevice()
	d.Tun = raw
	d.checkIdentity = func(bool) error { return errors.New("identity changed after open") }
	if d.Start() == nil || raw.starts != 0 {
		t.Fatal("unverified adapter was configured")
	}
}

// Keep the Xray interface at the fake boundary; these tests never call NewTun.
var _ tun.Tun = (*fakeNativeDevice)(nil)

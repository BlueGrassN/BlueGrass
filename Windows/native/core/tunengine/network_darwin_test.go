//go:build darwin

package tunengine

import (
	"context"
	"errors"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
	"net/netip"
	"testing"
)

func TestDarwinNetworkCancelledBeforeSystemReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SystemNetworkWithDNS(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := SystemNetworkWithOwned(ctx, nil, []OwnedPhysicalRoute{{Prefix: netip.MustParsePrefix("8.8.8.8/32"), Interface: "en0", Gateway: netip.MustParseAddr("192.168.1.1")}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := readSystemRoutes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDarwinRouteRIBDecoding(t *testing.T) {
	messages := []route.Message{
		&route.RouteMessage{Index: 4, Flags: unix.RTF_UP | unix.RTF_GATEWAY, Addrs: []route.Addr{
			unix.RTAX_DST: &route.Inet4Addr{}, unix.RTAX_GATEWAY: &route.Inet4Addr{IP: [4]byte{192, 168, 1, 1}}, unix.RTAX_NETMASK: nil,
		}},
		&route.RouteMessage{Index: 4, Flags: unix.RTF_UP, Addrs: []route.Addr{
			unix.RTAX_DST: &route.Inet4Addr{IP: [4]byte{192, 168, 1, 0}}, unix.RTAX_GATEWAY: &route.LinkAddr{Index: 4}, unix.RTAX_NETMASK: &route.Inet4Addr{IP: [4]byte{255, 255, 255, 0}},
		}},
	}
	got, err := darwinSystemRoutes(messages)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if got[0].Prefix.String() != "0.0.0.0/0" || got[0].NextHop.String() != "192.168.1.1" || got[1].Prefix.String() != "192.168.1.0/24" || !got[1].NextHop.IsUnspecified() {
		t.Fatal(got)
	}
	messages[0].(*route.RouteMessage).Flags |= unix.RTF_IFSCOPE
	got, err = darwinSystemRoutes(messages)
	if err != nil || len(got) != 1 {
		t.Fatal("scoped default became primary", got, err)
	}
	messages[1].(*route.RouteMessage).Flags |= unix.RTF_WASCLONED
	got, err = darwinSystemRoutes(messages)
	if err != nil || len(got) != 0 {
		t.Fatal("ephemeral cloned route changes generation", got, err)
	}
}

func TestDarwinPhysicalSelectionIgnoresVPNMoreSpecificRoute(t *testing.T) {
	a := []systemAdapter{{Name: "en0", LUID: 4, Up: true}, {Name: "utun0", LUID: 8, Up: true}, {Name: InterfaceName, LUID: 9, Up: true}}
	r := []systemRoute{{LUID: 4, Prefix: netip.MustParsePrefix("0.0.0.0/0")}, {LUID: 8, Prefix: netip.MustParsePrefix("8.8.8.8/32")}, {LUID: 9, Prefix: netip.MustParsePrefix("0.0.0.0/1")}}
	got, err := selectPhysicalInterface(netip.MustParseAddr("8.8.8.8"), a, r)
	if err != nil || got != "en0" {
		t.Fatal(got, err)
	}
	r = r[1:]
	if _, err = selectPhysicalInterface(netip.MustParseAddr("8.8.8.8"), a, r); err == nil {
		t.Fatal("VPN became physical fallback")
	}
}

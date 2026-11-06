//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"bluegrass/native/core/entrydns"
)

func TestControlResolutionBootstrapAndRemoteHostUsePhysicalDNSOnly(t *testing.T) {
	f := newCompositionFixture()
	if err := f.e.SetEntryDNS(customEntryPolicy()); err != nil {
		t.Fatal(err)
	}
	f.e.resolve = func(_ context.Context, host string, p entrydns.Policy, auto []netip.AddrPort) (netip.Addr, error) {
		f.log.add("resolve")
		if (host != ControlAPIHost && host != "api-next.sunip.vip") || p.Mode != "auto" || len(auto) != 1 || auto[0].String() != "9.9.9.9:53" || f.log.count("control-dns") < 1 {
			t.Fatal("control lookup was not preceded by physical DNS authorization")
		}
		return netip.MustParseAddr("8.8.4.4"), nil
	}
	for _, host := range []string{"localhost", "client-api.sunip.vip.evil.test", "https://client-api.sunip.vip", "8.8.4.4", "", "api.example:443", "-api.sunip.vip", "api..sunip.vip", "api.sunip.vip\r\nother"} {
		if _, err := f.e.ResolveControlHost(context.Background(), host); err == nil {
			t.Fatal("arbitrary host accepted", host)
		}
	}
	if len(f.log.snapshot()) != 0 {
		t.Fatal("rejected host reached network")
	}
	ips, err := f.e.ResolveControlHost(context.Background(), "client-api.sunip.vip")
	if err != nil || len(ips) != 1 || ips[0].String() != "8.8.4.4" || f.log.count("block") != 0 || f.log.count("arm") != 0 || f.log.count("release") != 0 {
		t.Fatal("control resolution changed business policy", ips, err)
	}
	if _, err := f.e.ResolveControlHost(context.Background(), "api-next.sunip.vip"); err != nil {
		t.Fatal("remote API hostname could not use the same protected resolver", err)
	}
}

func TestControlDNSAuthorizationFailureNeverResolves(t *testing.T) {
	f := newCompositionFixture()
	f.p.controlErr = errors.New("transaction failed")
	if _, err := f.e.ResolveControlHost(context.Background(), ControlAPIHost); err == nil || f.log.count("resolve") != 0 {
		t.Fatal("failed DNS permission still resolved")
	}
}

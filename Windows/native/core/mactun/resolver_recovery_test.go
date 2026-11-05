package mactun

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"bluegrass/native/core/tunengine"
)

func TestResolversIgnoreUnusedUpWiFiWithoutDHCP(t *testing.T) {
	services := []Resolver{{Service: "Ethernet", Interface: "en0"}, {Service: "Wi-Fi", Interface: "en1"}}
	u := Underlay{Interface: "en0", Source: netip.MustParseAddr("192.168.1.10")}
	loaded := []string{}
	result, err := collectResolvers(services, u, func(name string) (resolverInterface, error) {
		info := resolverInterface{Up: true, Hardware: true}
		if name == "en0" {
			info.Addresses = []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24")}
		}
		return info, nil
	}, func(r Resolver) (Resolver, error) {
		loaded = append(loaded, r.Interface)
		if r.Interface == "en1" {
			return r, errors.New("unused Wi-Fi has no DHCP")
		}
		r.Effective = []netip.Addr{netip.MustParseAddr("192.168.1.1")}
		return r, nil
	})
	if err != nil || len(result) != 1 || len(loaded) != 1 || loaded[0] != "en0" {
		t.Fatalf("healthy default blocked by unused Wi-Fi: %v %v %v", result, loaded, err)
	}
}

func TestParticipatingResolverStillRequiresVerifiableDNS(t *testing.T) {
	services := []Resolver{{Service: "Ethernet", Interface: "en0"}}
	u := Underlay{Interface: "en0", Source: netip.MustParseAddr("192.168.1.10")}
	for _, ip := range []string{"169.254.1.2/16", "127.0.0.1/8", "198.18.1.2/16", "2001:db8::1/64"} {
		called := false
		_, err := collectResolvers(services, u, func(string) (resolverInterface, error) {
			return resolverInterface{Up: true, Hardware: true, Addresses: []netip.Prefix{netip.MustParsePrefix(ip)}}, nil
		}, func(r Resolver) (Resolver, error) { called = true; return r, nil })
		if err == nil || called {
			t.Fatalf("invalid default source accepted: %s", ip)
		}
	}
	_, err := collectResolvers(services, u, func(string) (resolverInterface, error) {
		return resolverInterface{Up: true, Hardware: true, Addresses: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24")}}, nil
	}, func(r Resolver) (Resolver, error) { return r, errors.New("active DNS unverifiable") })
	if err == nil {
		t.Fatal("silently skipped unverifiable active resolver")
	}
}

type changingDNSBackend struct {
	*dnsBackend
	currentDNS netip.Addr
}

func (b *changingDNSBackend) Resolvers(context.Context) ([]Resolver, error) {
	return []Resolver{{Service: "Wi-Fi", Interface: "en0", Effective: []netip.Addr{b.currentDNS}, DHCP: []netip.Addr{b.currentDNS}}}, nil
}
func (b *changingDNSBackend) VerifyResolver(_ context.Context, r Resolver) error {
	if !sameAddresses(r.DHCP, []netip.Addr{b.currentDNS}) {
		return errors.New("NETWORK_CHANGED")
	}
	return nil
}
func (b *changingDNSBackend) Network(ctx context.Context, original map[string][]netip.Addr, owned []Route) (tunengine.NetworkSnapshot, error) {
	n, err := b.dnsBackend.Network(ctx, original, owned)
	ip := b.currentDNS
	if ips, ok := original["en0"]; ok && len(ips) > 0 {
		ip = ips[0]
	}
	n.EntryDNS = []netip.AddrPort{netip.AddrPortFrom(ip, 53)}
	return n, err
}

func TestPausedControlRebuildsDNSAndGenerationWithSameUnderlay(t *testing.T) {
	for _, change := range []string{"dhcp-only", "generation-only", "external-dns"} {
		t.Run(change, func(t *testing.T) {
			tun, base, _, err := fixture()
			if err != nil {
				t.Fatal(err)
			}
			b := &changingDNSBackend{dnsBackend: &dnsBackend{fakeBackend: base, answer: netip.MustParseAddr("9.9.9.9")}, currentDNS: netip.MustParseAddr("192.168.1.1")}
			tun.m.b = b
			if err = tun.Prepare(context.Background(), profile()); err != nil {
				t.Fatal(err)
			}
			if err = tun.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			if err = tun.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			originalUnderlay := tun.underlay
			if change == "generation-only" {
				base.generation++
			} else {
				b.currentDNS = netip.MustParseAddr("192.168.1.2")
			}
			foreign := []netip.Addr{netip.MustParseAddr("8.8.4.4")}
			if change == "external-dns" {
				base.dns["Wi-Fi"] = foreign
			}
			ips, err := tun.ResolveControlHost(context.Background(), "client-api.sunip.vip")
			if change == "external-dns" {
				if !errors.Is(err, ErrExternalWriter) || !sameAddresses(base.dns["Wi-Fi"], foreign) || len(tun.m.j.DNS) == 0 || len(b.dialed) != 0 {
					t.Fatalf("overrode external DNS or lost checkpoint: %v", err)
				}
			} else {
				if err != nil || len(ips) != 1 {
					t.Fatal(ips, err)
				}
				if len(tun.m.j.DNS) != 0 || len(base.dns["Wi-Fi"]) != 0 || tun.networkGeneration != base.generation {
					t.Fatal("stale resolver checkpoint/generation retained")
				}
				if len(b.dialed) == 0 || b.dialed[0].Addr() != b.currentDNS {
					t.Fatal("did not use fresh physical DNS", b.dialed)
				}
			}
			if tun.underlay != originalUnderlay || tun.Forwarding() || strings.Contains(base.rules, "bluegrass-tunnel") || !base.enabled {
				t.Fatal("bootstrap rebuild changed forwarding/protection")
			}
		})
	}
}

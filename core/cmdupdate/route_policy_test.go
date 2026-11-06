package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

const adapterFixture = "{11111111-2222-3333-4444-555555555555}"

func policyFixture(t *testing.T, sample *connectionCost, changed *atomic.Bool) (*routePolicy, *atomic.Int32) {
	t.Helper()
	if sample.NetworkGUID == "" {
		sample.NetworkGUID = "network-a"
	}
	dials := new(atomic.Int32)
	policy := newRoutePolicy([]string{"updates.example.test"}, func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}, func(_ context.Context, ip netip.Addr) (physicalBinding, error) {
		if ip.String() != "93.184.216.34" {
			t.Error("route destination not resolved public endpoint")
		}
		return physicalBinding{adapterGUID: adapterFixture, key: "verified-physical-route-a", verify: func(context.Context) error {
			if changed.Load() {
				return errors.New("route changed")
			}
			return nil
		}, dial: func(context.Context) (net.Conn, error) {
			dials.Add(1)
			client, server := net.Pipe()
			server.Close()
			return client, nil
		}}, nil
	}, func(_ context.Context, guid string) (connectionCost, error) {
		if guid != adapterFixture {
			t.Error("cost not bound to physical adapter GUID")
		}
		return *sample, nil
	})
	return policy, dials
}

func TestPhysicalPolicyUnknownAndMeteredWaitBeforeDialWhileExactConsentAllows(t *testing.T) {
	for _, flags := range []uint32{0, 1, 2, 4, 1 | 0x40000} {
		for _, consent := range []bool{false, true} {
			sample := connectionCost{AdapterGUID: adapterFixture, ConnectionGUID: "connection-a", Flags: flags}
			var changed atomic.Bool
			p, dials := policyFixture(t, &sample, &changed)
			p.beginDownload(consent)
			if err := p.prepare(context.Background(), "https://updates.example.test/setup.exe"); err != nil {
				t.Fatal(err)
			}
			conn, err := p.dialContext(context.Background(), "tcp", "updates.example.test:443")
			allowed := consent || flags == 1
			if allowed {
				if err != nil || conn == nil {
					t.Fatal("permitted physical connection failed", err)
				}
				conn.Close()
			} else if !errors.Is(err, update.ErrWaitingNetwork) || dials.Load() != 0 {
				t.Fatal("unapproved network reached dial", flags, err)
			}
		}
	}
}

func TestRouteAndNetworkProfileChangesPoisonAttemptEvenWithConsent(t *testing.T) {
	for _, mode := range []string{"route", "connection", "profile"} {
		t.Run(mode, func(t *testing.T) {
			sample := connectionCost{AdapterGUID: adapterFixture, ConnectionGUID: "connection-a", Flags: 1}
			var changed atomic.Bool
			p, dials := policyFixture(t, &sample, &changed)
			p.beginDownload(true)
			if p.prepare(context.Background(), "https://updates.example.test/setup.exe") != nil {
				t.Fatal("prepare")
			}
			if p.routeValid(context.Background()) != nil {
				t.Fatal("initial route")
			}
			if mode == "route" {
				changed.Store(true)
			} else if mode == "connection" {
				sample.ConnectionGUID = "connection-b"
			} else {
				sample.NetworkGUID = "network-b"
			}
			p.sampleAt = time.Time{} // force the next fixed sample, without sleeps
			if !errors.Is(p.routeValid(context.Background()), update.ErrWaitingNetwork) {
				t.Fatal("network change silently continued")
			}
			changed.Store(false)
			sample.ConnectionGUID = "connection-a"
			sample.NetworkGUID = "network-a"
			if _, err := p.dialContext(context.Background(), "tcp", "updates.example.test:443"); !errors.Is(err, update.ErrWaitingNetwork) || dials.Load() != 0 {
				t.Fatal("poisoned attempt recovered silently", err)
			}
		})
	}
}

func TestMismatchedAdapterCostNeverMarksRouteUnmetered(t *testing.T) {
	sample := connectionCost{AdapterGUID: "different-adapter", ConnectionGUID: "vpn-profile", Flags: 1}
	var changed atomic.Bool
	p, dials := policyFixture(t, &sample, &changed)
	p.beginDownload(false)
	if p.prepare(context.Background(), "https://updates.example.test/setup.exe") != nil {
		t.Fatal("prepare")
	}
	if cost, _ := p.network(context.Background()); cost != update.NetworkUnknown {
		t.Fatal("unrelated VPN/global cost accepted")
	}
	if _, err := p.dialContext(context.Background(), "tcp", "updates.example.test:443"); !errors.Is(err, update.ErrWaitingNetwork) || dials.Load() != 0 {
		t.Fatal("mismatched adapter reached network")
	}
}

func TestRecheckingCostDoesNotReuseOldUnmeteredSample(t *testing.T) {
	sample := connectionCost{AdapterGUID: adapterFixture, ConnectionGUID: "connection-a", Flags: 1}
	var changed atomic.Bool
	p, dials := policyFixture(t, &sample, &changed)
	p.beginDownload(false)
	if p.prepare(context.Background(), "https://updates.example.test/setup.exe") != nil {
		t.Fatal("prepare")
	}
	if cost, err := p.network(context.Background()); err != nil || cost != update.NetworkUnmetered {
		t.Fatal("initial unmetered sample", err)
	}
	sample.Flags = 2
	p.sampleAt = time.Time{}
	if p.routeValid(context.Background()) != nil {
		t.Fatal("cost-only change incorrectly lost physical route")
	}
	if cost, err := p.network(context.Background()); err != nil || cost != update.NetworkMetered {
		t.Fatal("stale unmetered sample survived recheck", err)
	}
	if _, err := p.dialContext(context.Background(), "tcp", "updates.example.test:443"); !errors.Is(err, update.ErrWaitingNetwork) || dials.Load() != 0 {
		t.Fatal("metered transition silently continued", err)
	}
}

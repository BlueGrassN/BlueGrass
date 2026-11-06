//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"bluegrass/native/core/tunengine"
)

func TestPreparedConnectionOpensBeforeOptionalExitObservation(t *testing.T) {
	for _, legacyExpected := range []string{"", "9.9.9.9", "legacy-invalid-metadata"} {
		t.Run("expected="+legacyExpected, func(t *testing.T) {
			f := newCompositionFixture()
			p := compositionProfile()
			p.ExpectedExitIPv4 = legacyExpected
			if err := f.e.Prepare(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if err := f.e.SetBusinessGate(true); err != nil {
				t.Fatal("prepared connection required exit observation", err)
			}
			events := f.log.snapshot()
			if f.log.count("probe") != 0 || f.e.VerifiedIPv4() != "" || !slices.Equal(events[len(events)-2:], []string{"gate", "allow-tunnel"}) {
				t.Fatal("gate did not open before optional observation", events)
			}
			for _, ip := range []string{"1.1.1.1", "8.8.8.8"} {
				f.tun.probeIP = ip
				result, err := f.e.ProbeExit(context.Background())
				if err != nil || result.IPv4 != ip {
					t.Fatal("selected observation lost", result, err)
				}
			}
			// The producer accepts only public IPv4. A rejected response (for
			// example IPv6 with no successful fallback) is advisory as well.
			for _, detectorErr := range []error{errors.New("exit detector unavailable"), errors.New("INVALID_EXIT_RESPONSE")} {
				f.tun.probeErr = detectorErr
				result, err := f.e.ProbeExit(context.Background())
				if !errors.Is(err, detectorErr) || result.IPv4 != "" {
					t.Fatal("advisory failure hidden or published an address", result, err)
				}
			}
			if f.log.count("block") != 1 || f.log.count("close-tun") != 0 || f.log.count("release") != 0 || !f.e.prepared || f.e.tun != f.tun {
				t.Fatal("exit observation changed connection or protection", f.log.snapshot())
			}
			if err := f.callbacks[0](netip.MustParseAddrPort("8.8.4.4:50000")); err != nil {
				t.Fatal("optional detector revoked the current relay session", err)
			}
		})
	}
}

func TestNetworkMonitoringDoesNotRequireExitObservation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation uint64
		err        error
		changed    bool
	}{
		{name: "same physical snapshot", generation: 1},
		{name: "changed physical snapshot", generation: 2, changed: true},
		{name: "snapshot failure", err: errors.New("snapshot unavailable"), changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCompositionFixture()
			f.prepare(t)
			if err := f.e.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) {
				calls++
				return tunengine.NetworkSnapshot{Generation: tc.generation}, tc.err
			}
			if got := f.e.networkChanged(); got != tc.changed || calls != 1 || f.log.count("probe") != 0 {
				t.Fatalf("network check depended on exit observation: changed=%v calls=%d", got, calls)
			}
		})
	}
}

func TestGateAndPermissionFailuresStillRevokeUnobservedSession(t *testing.T) {
	for _, failure := range []string{"engine gate", "WFP interface permission", "cancelled session"} {
		t.Run(failure, func(t *testing.T) {
			f := newCompositionFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := f.e.Prepare(ctx, compositionProfile()); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "engine gate":
				f.tun.gateErr = errors.New("gate failed")
			case "WFP interface permission":
				f.p.tunnelPermitErr = errors.New("permission failed")
			case "cancelled session":
				cancel()
			}
			if err := f.e.SetBusinessGate(true); err == nil {
				t.Fatal("actual activation failure accepted")
			}
			if f.e.prepared || f.e.tun != nil || f.log.count("block") != 2 || f.log.count("close-tun") != 1 || f.log.count("release") != 0 || !f.e.Active() {
				t.Fatal("actual failure lost blocking protection", f.log.snapshot())
			}
			events := f.log.snapshot()
			if slices.Index(events[1:], "block")+1 >= slices.Index(events, "close-tun") {
				t.Fatal("runtime closed before WFP revocation", events)
			}
			if err := f.callbacks[0](netip.MustParseAddrPort("8.8.4.4:50000")); err == nil || f.log.count("relay") != 0 {
				t.Fatal("failed activation left an authorized relay callback")
			}
		})
	}
}

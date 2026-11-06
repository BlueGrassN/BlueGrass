//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
)

func customEntryPolicy() entrydns.Policy {
	return entrydns.Policy{Mode: "custom", Candidates: []entrydns.Candidate{
		{Address: "1.1.1.1", Transport: "tcp"},
		{Address: "8.8.8.8", Port: 5353, Transport: "udp"},
	}}
}

func TestEntryDNSCustomPolicyUsesExactBootstrapTransports(t *testing.T) {
	f := newCompositionFixture()
	policy := customEntryPolicy()
	if err := f.e.SetEntryDNS(policy); err != nil {
		t.Fatal(err)
	}
	want := []winprotect.DNSEndpoint{
		{Endpoint: netip.MustParseAddrPort("1.1.1.1:53"), Transport: "tcp"},
		{Endpoint: netip.MustParseAddrPort("8.8.8.8:5353"), Transport: "udp"},
	}
	f.e.resolve = func(_ context.Context, _ string, p entrydns.Policy, auto []netip.AddrPort) (netip.Addr, error) {
		f.log.add("resolve")
		candidates, err := p.Effective(auto)
		if err != nil || !slices.Equal(candidates, customEntryPolicy().Candidates) || !slices.Equal(f.p.bootstrap, want) {
			t.Fatal("resolver policy differs from committed bootstrap or added fallback", candidates, err)
		}
		return netip.MustParseAddr("8.8.4.4"), nil
	}
	// Caller mutation after SetEntryDNS must not change either DNS permissions or resolution.
	policy.Candidates[0].Address = "4.4.4.4"
	f.prepare(t)
	if !slices.Equal(f.p.dns, want) {
		t.Fatal("full Arm broadened custom candidates", f.p.dns)
	}
	before := f.log.snapshot()
	if err := f.e.SetEntryDNS(entrydns.Policy{Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, f.log.snapshot()) || !slices.Equal(f.p.dns, want) {
		t.Fatal("next-policy setter mutated current session")
	}
	f.e.resolve = func(_ context.Context, _ string, p entrydns.Policy, auto []netip.AddrPort) (netip.Addr, error) {
		if p.Mode != "auto" || len(auto) != 1 || auto[0].String() != "9.9.9.9:53" {
			t.Fatal("next Prepare lost updated policy")
		}
		return netip.MustParseAddr("8.8.4.4"), nil
	}
	f.prepare(t)
	if len(f.p.bootstrap) != 6 || f.p.bootstrap[0].Endpoint.String() != "9.9.9.9:53" {
		t.Fatal("auto policy not applied next Prepare")
	}
}

func TestEntryDNSLocalResolversOnlyJoinFullArmWhenLANEnabled(t *testing.T) {
	for _, allow := range []bool{false, true} {
		f := newCompositionFixture()
		f.e.allowLAN = allow
		f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) {
			return tunengine.NetworkSnapshot{EntryDNS: []netip.AddrPort{netip.MustParseAddrPort("9.9.9.9:53")},
				DNS: []tunengine.LocalDNS{{Server: netip.MustParseAddrPort("192.168.1.1:53")}}}, nil
		}
		if err := f.e.SetEntryDNS(customEntryPolicy()); err != nil {
			t.Fatal(err)
		}
		f.prepare(t)
		if len(f.p.bootstrap) != 2 {
			t.Fatal("bootstrap added local or automatic resolver")
		}
		want := 2
		if allow {
			want = 4
		}
		if len(f.p.dns) != want {
			t.Fatal("LAN DNS permission does not match LAN policy", allow, f.p.dns)
		}
	}
}

func TestLiteralEntryNeedsNoAutomaticDNS(t *testing.T) {
	f := newCompositionFixture()
	f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) { return tunengine.NetworkSnapshot{}, nil }
	f.e.resolve = entrydns.Resolve // the real resolver takes the literal path without sockets
	p := compositionProfile()
	p.Entry.Host = "8.8.4.4"
	if err := f.e.Prepare(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(f.p.bootstrap) != 0 || len(f.p.dns) != 0 || f.log.count("arm-dns") != 1 || f.log.count("arm") != 1 {
		t.Fatal("literal bypassed protection or invented DNS permissions")
	}
}

func TestEntryDNSRejectsUnrepresentableCustomAndArmsExplicitAutoBackups(t *testing.T) {
	for _, address := range []string{"fe80::1", "fe80::1%12", "169.254.1.1"} {
		f := newCompositionFixture()
		p := entrydns.Policy{Mode: "custom", Candidates: []entrydns.Candidate{{Address: address, Transport: "udp"}}}
		if f.e.SetEntryDNS(p) == nil || len(f.log.snapshot()) != 0 {
			t.Fatal("accepted unrepresentable custom resolver", address)
		}
	}
	f := newCompositionFixture()
	f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) {
		return tunengine.NetworkSnapshot{EntryDNS: []netip.AddrPort{netip.MustParseAddrPort("[fe80::1]:53")}}, nil
	}
	if f.e.Prepare(context.Background(), compositionProfile()) != nil || f.log.count("resolve") != 1 || f.log.count("arm") != 1 || len(f.p.bootstrap) != 4 || f.p.bootstrap[0].Endpoint.String() != "1.1.1.1:53" {
		t.Fatal("automatic public backups were not explicitly armed before resolution")
	}
}

func TestFailedBootstrapNeverInvokesResolver(t *testing.T) {
	f := newCompositionFixture()
	f.p.bootstrapErr = errors.New("transaction failed")
	if f.e.Prepare(context.Background(), compositionProfile()) == nil || f.log.count("resolve") != 0 || f.log.count("new-tun") != 0 {
		t.Fatal("resolution started without committed blocking")
	}
}

func TestFailedCustomResolverDoesNotTrySystemPolicy(t *testing.T) {
	f := newCompositionFixture()
	if err := f.e.SetEntryDNS(customEntryPolicy()); err != nil {
		t.Fatal(err)
	}
	f.e.resolve = func(_ context.Context, _ string, p entrydns.Policy, _ []netip.AddrPort) (netip.Addr, error) {
		f.log.add("resolve")
		if p.Mode != "custom" {
			t.Fatal("custom failure fell back to auto")
		}
		return netip.Addr{}, errors.New("resolution failed")
	}
	if f.e.Prepare(context.Background(), compositionProfile()) == nil || f.log.count("resolve") != 1 || f.log.count("arm") != 0 || f.log.count("release") != 0 {
		t.Fatal("resolver failure did not retain bootstrap blocking")
	}
}

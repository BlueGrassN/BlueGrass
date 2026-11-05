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

type localTunnelFixture struct {
	log                *compositionLog
	closeErr, startErr error
	startContext       context.Context
	onStart            func(context.Context)
}

func (l *localTunnelFixture) Start(ctx context.Context) error {
	l.log.add("start-local")
	l.startContext = ctx
	if l.onStart != nil {
		l.onStart(ctx)
	}
	return l.startErr
}
func (l *localTunnelFixture) TunnelLUID() uint64 { return 43 }
func (l *localTunnelFixture) Close() error       { l.log.add("close-local"); return l.closeErr }

func (p *protectionFixture) ArmLocalDNS(_ string, dns []winprotect.DNSEndpoint, _ bool) error {
	p.log.add("arm-local")
	p.dns = append([]winprotect.DNSEndpoint(nil), dns...)
	return nil
}
func (p *protectionFixture) BeginLocalRuntime(sid string) error {
	p.log.add("journal-local")
	if p.beginErr != nil {
		return p.beginErr
	}
	if p.checkpoint.OwnerSID != sid || p.checkpoint.CleanupRequired {
		return winprotect.ErrRecoveryRequired
	}
	p.checkpoint.CleanupRequired = true
	p.checkpoint.Phase = "local"
	p.checkpoint.Revision++
	return nil
}
func (p *protectionFixture) AllowLocalTunnel(string, uint64) error {
	p.log.add("allow-local")
	return p.localPermitErr
}

func TestPauseBeginsSeparateLocalRuntimeAfterBusinessCleanup(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.log.count("arm-local") != 1 {
		t.Fatal("pause lost independent local DNS runtime", f.log.snapshot())
	}
	want := []string{"journal-maintenance", "block", "close-tun", "journal-clean", "arm-local", "journal-local", "new-local", "start-local", "allow-local"}
	events := f.log.snapshot()
	if !slices.Equal(events[len(events)-len(want):], want) || !f.p.checkpoint.CleanupRequired || f.p.checkpoint.Phase != "local" || f.p.checkpoint.LogicalSessionID != "logical-service-1" {
		t.Fatal("unsafe local transition", events, f.p.checkpoint)
	}
	if f.log.count("allow-tunnel") != 0 || f.log.count("clear-cache") != 0 || f.e.SetBusinessGate(true) == nil {
		t.Fatal("pause opened business or erased cache")
	}
	if err := f.e.Stop(context.Background()); err != nil || f.log.count("new-local") != 1 {
		t.Fatal("Stop recreated an already-live local runtime", err)
	}
	if err := f.e.Release(); err != nil {
		t.Fatal(err)
	}
	events = f.log.snapshot()
	want = []string{"journal-maintenance", "block", "close-local", "journal-clean", "clear-cache", "release"}
	if !slices.Equal(events[len(events)-len(want):], want) || f.e.localDesired || f.e.local != nil {
		t.Fatal("release did not close local before erasing intent", events)
	}
}

func TestLocalDNSOutlivesCanceledAttemptAndRestoresAfterEntryFailure(t *testing.T) {
	f := newCompositionFixture()
	ctx, cancel := context.WithCancel(context.Background())
	f.e.resolve = func(context.Context, string, entrydns.Policy, []netip.AddrPort) (netip.Addr, error) {
		cancel()
		return netip.Addr{}, errors.New("entry resolver failed")
	}
	f.e.newLocal = func(c tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) {
		if !f.p.checkpoint.CleanupRequired || f.p.checkpoint.Phase != "local" {
			t.Fatal("constructor preceded journal")
		}
		return &localTunnelFixture{log: f.log, onStart: func(localCtx context.Context) {
			if ctx.Err() == nil || localCtx.Err() != nil {
				t.Fatal("local startup inherited canceled attempt")
			}
		}}, nil
	}
	if err := f.e.Prepare(ctx, compositionProfile()); err == nil || f.e.local == nil || f.e.prepared || f.log.count("allow-tunnel") != 0 || f.log.count("allow-local") != 1 {
		t.Fatal("entry failure lost independent local DNS", err, f.log.snapshot())
	}
	if err := f.e.Close(); err != nil {
		t.Fatal(err)
	}
	before := len(f.log.snapshot())
	f.e.refreshLocal(context.Background())
	if len(f.log.snapshot()) != before || f.e.local != nil || f.log.count("clear-cache") != 0 {
		t.Fatal("service close restarted local runtime or erased cache")
	}
}

func TestLocalRefreshRevokesOldPolicyBeforeNewSnapshotAndRejectsScopedDNS(t *testing.T) {
	f := newCompositionFixture()
	var generation uint64 = 1
	f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) {
		return tunengine.NetworkSnapshot{Generation: generation, EntryDNS: []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:53")}, DNS: []tunengine.LocalDNS{
			{Server: netip.MustParseAddrPort("192.168.1.1:53"), Interface: "Ethernet", Domains: []string{"lan"}},
			{Server: netip.MustParseAddrPort("[fe80::1%Ethernet]:53"), Interface: "Ethernet", Domains: []string{"home.arpa"}},
		}}, nil
	}
	var snapshots []tunengine.NetworkSnapshot
	f.e.newLocal = func(c tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) {
		f.log.add("new-local")
		snapshots = append(snapshots, c.Network)
		return &localTunnelFixture{log: f.log}, nil
	}
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(snapshots[0].DNS) != 1 || len(snapshots[0].EntryDNS) != 0 || len(f.p.dns) != 1 || f.p.dns[0].Transport != "tcp" {
		t.Fatal("local runtime got public fallback or unrepresentable DNS", snapshots, f.p.dns)
	}
	before := len(f.log.snapshot())
	generation = 2
	f.e.refreshLocal(context.Background())
	want := []string{"journal-maintenance", "block", "close-local", "journal-clean", "arm-local", "journal-local", "new-local", "start-local", "allow-local"}
	if !slices.Equal(f.log.snapshot()[before:], want) || snapshots[1].Generation != 2 {
		t.Fatal("network refresh did not revoke before replacement", f.log.snapshot()[before:])
	}
	f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) {
		return tunengine.NetworkSnapshot{}, errors.New("snapshot unavailable")
	}
	f.e.refreshLocal(context.Background())
	if f.e.local != nil || f.log.count("new-local") != 2 {
		t.Fatal("unreadable network reused stale runtime")
	}
}

func TestLocalFailedCleanupCannotRearmReleaseOrDropOwnership(t *testing.T) {
	f := newCompositionFixture()
	l := &localTunnelFixture{log: f.log}
	f.e.newLocal = func(tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) { f.log.add("new-local"); return l, nil }
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.closeErr = errors.New("sticky local cleanup")
	if !errors.Is(f.e.Release(), l.closeErr) || f.e.local != l || !f.p.checkpoint.CleanupRequired || f.log.count("clear-cache") != 0 || f.log.count("release") != 0 {
		t.Fatal("failed local cleanup was erased")
	}
	if !errors.Is(f.e.Prepare(context.Background(), compositionProfile()), l.closeErr) || f.log.count("arm") != 0 || f.log.count("new-local") != 1 {
		t.Fatal("rearm replaced unconfirmed local runtime")
	}
	l.closeErr = nil
	if err := f.e.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalJournalAndPermissionFailuresNeverClaimLiveRuntime(t *testing.T) {
	for _, stage := range []string{"journal", "permit", "start-close", "confirm"} {
		t.Run(stage, func(t *testing.T) {
			f := newCompositionFixture()
			f.prepare(t)
			switch stage {
			case "journal":
				f.p.beginErr = errors.New("disk full")
			case "permit":
				f.p.localPermitErr = errors.New("WFP transaction failed")
			case "confirm":
				f.p.confirmErr = errors.New("cleanup write failed")
			case "start-close":
				f.e.newLocal = func(tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) {
					f.log.add("new-local")
					return &localTunnelFixture{log: f.log, startErr: errors.New("start failed"), closeErr: errors.New("close failed")}, nil
				}
			}
			if f.e.Stop(context.Background()) == nil {
				t.Fatal("failure reported success")
			}
			if stage == "journal" || stage == "confirm" {
				if f.log.count("new-local") != 0 {
					t.Fatal("created local runtime without journal/cleanup")
				}
			}
			if stage == "permit" && (f.e.local != nil || f.log.count("close-local") != 1) {
				t.Fatal("failed permit did not close exact runtime")
			}
			if stage == "start-close" && (f.e.local == nil || !f.p.checkpoint.CleanupRequired || f.log.count("allow-local") != 0) {
				t.Fatal("lost unconfirmed partial local runtime")
			}
		})
	}
}

func TestLocalRetiredRefreshCannotTouchNewBusinessAndLANOffRebuilds(t *testing.T) {
	f := newCompositionFixture()
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.e.SetAllowLAN(false); err != nil {
		t.Fatal(err)
	}
	if f.log.count("close-local") != 1 || f.log.count("new-local") != 2 || len(f.p.dns) != 0 {
		t.Fatal("LAN change retained resolver permissions")
	}
	f.prepare(t)
	before := len(f.log.snapshot())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.e.refreshLocal(ctx)
	if !f.e.prepared || f.e.local != nil || len(f.log.snapshot()) != before {
		t.Fatal("retired watcher harmed business runtime")
	}
}

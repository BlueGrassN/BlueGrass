//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"
)

func (p *protectionFixture) RemoveUDPRelay(netip.AddrPort) error {
	p.log.add("remove-relay")
	return nil
}

type relayRemovalProtection struct {
	*protectionFixture
	removed  []netip.AddrPort
	hook     func()
	err      error
	blockErr error
}

func (p *relayRemovalProtection) RemoveUDPRelay(endpoint netip.AddrPort) error {
	if p.hook != nil {
		p.hook()
	}
	p.log.add("remove-relay")
	p.removed = append(p.removed, endpoint)
	return p.err
}

func (p *relayRemovalProtection) Block() error {
	p.log.add("block")
	return p.blockErr
}

func relayReleaseCallback(t *testing.T, f *compositionFixture) func(netip.AddrPort) error {
	t.Helper()
	if len(f.releaseCallbacks) == 0 || f.releaseCallbacks[len(f.releaseCallbacks)-1] == nil {
		t.Fatal("service did not wire the session-bound relay release callback")
	}
	return f.releaseCallbacks[len(f.releaseCallbacks)-1]
}

func TestRelayReleaseChecksSessionAndAllowsCanceledCleanup(t *testing.T) {
	f := newCompositionFixture()
	p := &relayRemovalProtection{protectionFixture: f.p}
	f.e.protection = p
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := f.e.Prepare(ctx, compositionProfile()); err != nil {
		t.Fatal(err)
	}
	release := relayReleaseCallback(t, f)
	relay := netip.MustParseAddrPort("8.8.4.4:50000")
	if err := release(relay); err != nil || len(p.removed) != 0 {
		t.Fatal("unactivated session reached relay removal", err)
	}
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if err := f.callbacks[0](relay); err != nil {
		t.Fatal(err)
	}
	cancel()
	if f.callbacks[0](relay) == nil {
		t.Fatal("canceled session authorized another relay")
	}
	if err := release(relay); err != nil {
		t.Fatal("canceled current session could not release", err)
	}
	if !slices.Equal(p.removed, []netip.AddrPort{relay}) {
		t.Fatal("release changed the endpoint or lost cleanup", p.removed)
	}
	f.prepare(t)
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if err := f.callbacks[1](relay); err != nil {
		t.Fatal(err)
	}
	if err := release(relay); err != nil || len(p.removed) != 1 {
		t.Fatal("old generation removed a new session's relay", err)
	}
	current := relayReleaseCallback(t, f)
	if err := current(relay); err != nil {
		t.Fatal(err)
	}
	if len(p.removed) != 2 || p.removed[1] != relay {
		t.Fatal("current release did not reach the exact relay")
	}
}

func TestRelayReleaseDuringCloseDoesNotDeadlockOrTouchRetiredPolicy(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.4.4:50000")
	if err := f.callbacks[0](relay); err != nil {
		t.Fatal(err)
	}
	release := relayReleaseCallback(t, f)
	callbackDone := make(chan error, 1)
	f.tun.onClose = func() { callbackDone <- release(relay) }
	closed := make(chan error, 1)
	go func() { closed <- f.e.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked with association release")
	}
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
	if f.log.count("remove-relay") != 0 {
		t.Fatal("association close changed already-retired policy")
	}
}

func TestRelayReleaseCompletesBeforeBlockAndNewArm(t *testing.T) {
	f := newCompositionFixture()
	entered, resume := make(chan struct{}), make(chan struct{})
	p := &relayRemovalProtection{protectionFixture: f.p, hook: func() { close(entered); <-resume }}
	f.e.protection = p
	f.prepare(t)
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.4.4:50000")
	if err := f.callbacks[0](relay); err != nil {
		t.Fatal(err)
	}
	release := relayReleaseCallback(t, f)
	released := make(chan error, 1)
	go func() { released <- release(relay) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("release did not reach protection")
	}
	prepared := make(chan error, 1)
	go func() { prepared <- f.e.Prepare(context.Background(), compositionProfile()) }()
	close(resume)
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-prepared:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rearm deadlocked with relay release")
	}
	events := f.log.snapshot()
	i := slices.Index(events, "remove-relay")
	if i < 0 || !slices.Equal(events[i+1:], []string{"journal-maintenance", "block", "close-tun", "journal-clean", "arm-dns", "resolve", "arm", "journal-runtime", "new-tun", "prepare"}) {
		t.Fatalf("release crossed policy generation: %v", events)
	}
}

func TestRelayReleaseFailureBlocksAndInvalidatesSession(t *testing.T) {
	for _, blockFails := range []bool{false, true} {
		name := "blocked"
		if blockFails {
			name = "block-failed"
		}
		t.Run(name, func(t *testing.T) {
			f := newCompositionFixture()
			p := &relayRemovalProtection{protectionFixture: f.p}
			f.e.protection = p
			f.prepare(t)
			if err := f.e.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			relay := netip.MustParseAddrPort("8.8.4.4:50000")
			if err := f.callbacks[0](relay); err != nil {
				t.Fatal(err)
			}
			release := relayReleaseCallback(t, f)
			p.err = errors.New("WFP removal failed")
			if blockFails {
				p.blockErr = errors.New("WFP block failed")
			}
			blocks := f.log.count("block")
			err := release(relay)
			if !errors.Is(err, p.err) || (p.blockErr != nil && !errors.Is(err, p.blockErr)) {
				t.Fatal("release lost removal or blocking failure", err)
			}
			if f.log.count("block") != blocks+1 {
				t.Fatal("failed removal did not attempt fail-closed revocation")
			}
			if f.callbacks[0](relay) == nil || f.log.count("relay") != 1 {
				t.Fatal("failed removal left session authorization usable")
			}
			if f.e.SetBusinessGate(true) == nil {
				t.Fatal("failed removal re-enabled the canceled session")
			}
		})
	}
}

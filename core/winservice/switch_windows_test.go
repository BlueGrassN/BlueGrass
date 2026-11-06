//go:build windows

package winservice

import (
	"context"
	"errors"
	"testing"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
)

type logicalSwitcher interface {
	SwitchLogicalSession(engine.ResourceKey, engine.ResourceKey, string) error
}

func (p *protectionFixture) SwitchLogicalSession(oldKey, newKey engine.ResourceKey, newID string) error {
	p.log.add("switch-session")
	if p.switchErr != nil {
		return p.switchErr
	}
	if p.checkpoint.CleanupRequired || p.checkpoint.ResourceKey != oldKey {
		return winprotect.ErrRecoveryRequired
	}
	p.checkpoint.ResourceKey, p.checkpoint.LogicalSessionID = newKey, newID
	p.checkpoint.Revision++
	return nil
}

func TestServiceExplicitSwitchClosesLocalAndRetainsProtection(t *testing.T) {
	f := newCompositionFixture()
	s, ok := any(f.e).(logicalSwitcher)
	if !ok {
		t.Fatal("missing service switch interface")
	}
	f.prepare(t)
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := compositionProfile().Key
	next.SourceID = "resource-2"
	if err := s.SwitchLogicalSession(compositionProfile().Key, next, "logical-service-2"); err != nil {
		t.Fatal(err)
	}
	if f.e.tun != nil || f.e.local != nil || f.e.boundKey != next || f.e.boundLogicalID != "logical-service-2" || f.log.count("clear-cache") != 0 || f.log.count("release") != 0 {
		t.Fatal("switch released or retained old runtime")
	}
	profile := compositionProfile()
	profile.Key = next
	if err := f.e.Prepare(context.Background(), profile); err != nil {
		t.Fatal("new full binding cannot prepare", err)
	}
	if f.log.count("allow-tunnel") != 0 {
		t.Fatal("switch reused old business proof")
	}
}

func TestServiceSwitchRejectsOtherSIDScopeAndRetainsUnconfirmedLocal(t *testing.T) {
	f := newCompositionFixture()
	next := compositionProfile().Key
	next.SourceID = "resource-2"
	l := &localTunnelFixture{log: f.log}
	f.e.newLocal = func(tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) { return l, nil }
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(f.log.snapshot())
	bad := next
	bad.AccountKey = "other"
	if f.e.SwitchLogicalSession(compositionProfile().Key, bad, "logical-2") == nil || len(f.log.snapshot()) != before {
		t.Fatal("cross-account switch altered runtime")
	}
	oldSID := f.e.sid
	f.e.sid = "S-1-5-21-9-9-9-1001"
	if f.e.SwitchLogicalSession(compositionProfile().Key, next, "logical-2") == nil || len(f.log.snapshot()) != before {
		t.Fatal("cross-SID switch altered runtime")
	}
	f.e.sid = oldSID
	l.closeErr = errors.New("local cleanup unconfirmed")
	if !errors.Is(f.e.SwitchLogicalSession(compositionProfile().Key, next, "logical-2"), l.closeErr) || f.log.count("switch-session") != 0 || f.e.local != l || f.e.boundKey != compositionProfile().Key {
		t.Fatal("switch erased unconfirmed local runtime")
	}
	l.closeErr = nil
	f.p.switchErr = errors.New("journal write failed")
	if f.e.SwitchLogicalSession(compositionProfile().Key, next, "logical-2") == nil || f.e.boundKey != compositionProfile().Key || f.e.boundLogicalID != "logical-service-1" || f.e.local != nil || f.log.count("release") != 0 {
		t.Fatal("failed switch changed binding or released WFP")
	}
}

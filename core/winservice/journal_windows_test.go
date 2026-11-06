//go:build windows

package winservice

import (
	"context"
	"errors"
	"testing"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/tunengine"
)

type logicalSessionMethods interface {
	ResumeLogicalSession(engine.ResourceKey) (string, error)
	BindLogicalSession(engine.ResourceKey, string) error
}

func TestServiceLogicalBindingUsesAuthenticatedSIDAndFullKey(t *testing.T) {
	f := newCompositionFixture()
	methods, ok := any(f.e).(logicalSessionMethods)
	if !ok {
		t.Fatal("missing optional logical session methods")
	}
	if err := methods.BindLogicalSession(compositionProfile().Key, "logical-service-1"); err != nil {
		t.Fatal(err)
	}
	if f.p.checkpoint.OwnerSID != f.e.sid {
		t.Fatal("SID was not service-derived")
	}
	key := compositionProfile().Key
	key.Environment = "other"
	if id, err := methods.ResumeLogicalSession(key); err == nil || id != "" {
		t.Fatal("different full key resumed")
	}
	if methods.BindLogicalSession(compositionProfile().Key, "caller-override") == nil {
		t.Fatal("caller changed persisted logical ID")
	}
	id, err := methods.ResumeLogicalSession(compositionProfile().Key)
	if err != nil || id != "logical-service-1" {
		t.Fatal(id, err)
	}
}

func TestServiceRuntimeJournalFailurePreventsAnyRuntimeCreation(t *testing.T) {
	f := newCompositionFixture()
	f.p.beginErr = errors.New("disk full")
	if f.e.Prepare(context.Background(), compositionProfile()) == nil || f.log.count("new-tun") != 0 || f.log.count("release") != 0 {
		t.Fatal("runtime started before durable journal")
	}
}

func TestServiceJournalIsDirtyBeforeFactoryAndCleanOnlyAfterClose(t *testing.T) {
	f := newCompositionFixture()
	factory := f.e.newTun
	f.e.newTun = func(c tunengine.Config) (ownedTunnel, error) {
		if !f.p.checkpoint.CleanupRequired {
			t.Fatal("factory called without checkpoint")
		}
		return factory(c)
	}
	f.prepare(t)
	f.tun.onClose = func() {
		if !f.p.checkpoint.CleanupRequired || f.p.checkpoint.Phase != "maintenance" {
			t.Fatal("cleanup preceded durable maintenance")
		}
	}
	if err := f.e.Close(); err != nil {
		t.Fatal(err)
	}
	if f.p.checkpoint.CleanupRequired || f.p.checkpoint.LogicalSessionID != "logical-service-1" {
		t.Fatal("cleanup erased session or failed to confirm")
	}
}

func TestServiceRestartRequiresExactRepairAndDurableConfirmationBeforeRelease(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	// A new service wrapper has no old Go object; the durable backend survives.
	next := newProtectedTunnel(f.p, f.e.sid, f.e.uiExecutable)
	next.cachePath, next.cachePathErr = f.e.cachePath, nil
	next.clearCache = func(string) error { f.log.add("clear-cache"); return nil }
	repairFailure := errors.New("adapter removal unconfirmed")
	next.repairAdapter = func() error { f.log.add("repair-adapter"); return repairFailure }
	if next.Release() == nil || !f.p.checkpoint.CleanupRequired || f.log.count("release") != 0 || f.log.count("clear-cache") != 0 {
		t.Fatal("nil runtime erased replay cleanup")
	}
	next.repairAdapter = func() error { f.log.add("repair-adapter"); return nil }
	f.p.confirmErr = errors.New("confirmation write failed")
	if next.Release() == nil || !f.p.checkpoint.CleanupRequired || f.log.count("release") != 0 {
		t.Fatal("failed confirmation released")
	}
	f.p.confirmErr = nil
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
	if f.log.count("release") != 1 || f.log.count("clear-cache") != 1 || f.p.checkpoint.LogicalSessionID != "" {
		t.Fatal("explicit release did not clear checkpoint")
	}
}

func TestServiceMaintenanceWriteFailureRetainsPendingEvenAfterClose(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	f.p.maintenanceErr = errors.New("disk unavailable")
	if f.e.Stop(context.Background()) == nil || f.log.count("close-tun") != 1 || !f.p.checkpoint.CleanupRequired || f.log.count("journal-clean") != 0 {
		t.Fatal("cleanup failure lost durable pending")
	}
	if f.e.Release() == nil || f.log.count("repair-adapter") != 0 || f.log.count("release") != 0 {
		t.Fatal("repair ran without maintenance journal")
	}
	f.p.maintenanceErr = nil
	if err := f.e.Release(); err != nil {
		t.Fatal(err)
	}
	if f.log.count("repair-adapter") != 1 {
		t.Fatal("lost-runtime cleanup was merely assumed")
	}
}

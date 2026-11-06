package nativehost

import (
	"context"
	"errors"
	"testing"

	"bluegrass/native/core/engine"
)

type tunnelStub struct {
	releaseCalls int
	active       bool
	releaseError error
}

func (*tunnelStub) Prepare(context.Context, engine.Profile) error { return nil }
func (*tunnelStub) ProbeExit(context.Context) (engine.ProbeResult, error) {
	return engine.ProbeResult{}, errors.New("closed")
}
func (*tunnelStub) SetBusinessGate(bool) error { return nil }
func (*tunnelStub) Stop(context.Context) error { return nil }
func (*tunnelStub) Close() error               { return nil }
func (*tunnelStub) Address() string            { return "" }
func (*tunnelStub) VerifiedIPv4() string       { return "" }
func (*tunnelStub) Counters() (uint64, uint64) { return 0, 0 }
func (e *tunnelStub) Active() bool             { return e.active }
func (e *tunnelStub) Release() error {
	e.releaseCalls++
	if e.releaseError == nil {
		e.active = false
	}
	return e.releaseError
}
func (*tunnelStub) SetAllowLAN(bool) error { return nil }

func TestRecoveredTunnelReportsProtectionUntilExplicitRelease(t *testing.T) {
	e := &tunnelStub{active: true}
	h := NewTunnel(e)
	hello := h.Handle(Request{ID: "hello", Command: "Hello"})
	if hello.Snapshot["protectionIntent"] != true || hello.Snapshot["businessGateOpen"] != false || hello.Snapshot["actualMode"] != "tun" {
		t.Fatal("hidden recovered protection", hello)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if e.releaseCalls != 0 || !e.active {
		t.Fatal("process close released persistent protection")
	}
	h = NewTunnel(e)
	reply := h.Handle(Request{ID: "release", Command: "Disconnect"})
	if !reply.OK || e.active || reply.Snapshot["protectionIntent"] != false {
		t.Fatal("explicit recovery failed", reply)
	}
}
func TestTunnelReleaseFailureCannotClaimRecovery(t *testing.T) {
	e := &tunnelStub{active: true, releaseError: errors.New("failed")}
	h := NewTunnel(e)
	defer h.Close()
	reply := h.Handle(Request{ID: "release", Command: "ReleaseProtection"})
	if reply.OK || reply.Snapshot["protectionIntent"] != true {
		t.Fatal("failed release reported success", reply)
	}
}

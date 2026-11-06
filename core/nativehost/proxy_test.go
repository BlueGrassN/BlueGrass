package nativehost

import (
	"context"
	"errors"
	"testing"

	"bluegrass/native/core/engine"
)

type gateEngine struct{ open, verified bool }

func (e *gateEngine) Prepare(context.Context, engine.Profile) error { return nil }
func (e *gateEngine) ProbeExit(context.Context) (engine.ProbeResult, error) {
	return engine.ProbeResult{}, nil
}
func (e *gateEngine) Stop(context.Context) error { e.open = false; return nil }
func (e *gateEngine) SetBusinessGate(open bool) error {
	if open && !e.verified {
		return errors.New("not verified")
	}
	e.open = open
	return nil
}

type testProxy struct {
	applied                        bool
	applies, restores              int
	applyErr, restoreErr, stateErr error
}

func (p *testProxy) Apply(string, string) error { p.applies++; p.applied = true; return p.applyErr }
func (p *testProxy) Restore() error {
	p.restores++
	if p.restoreErr == nil {
		p.applied = false
	}
	return p.restoreErr
}
func (p *testProxy) Relinquish() error    { return p.Restore() }
func (p *testProxy) State() (bool, error) { return p.applied, p.stateErr }

func TestProxyAppliesOnlyAfterExitVerification(t *testing.T) {
	base, proxy := &gateEngine{}, &testProxy{}
	e := &proxyEngine{Engine: base, manager: proxy, address: "127.0.0.1:49152"}
	if e.SetBusinessGate(true) == nil || proxy.applies != 0 {
		t.Fatal("unverified exit changed system proxy")
	}
	base.verified = true
	if err := e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if !base.open || !proxy.applied {
		t.Fatal("verified connection did not apply system proxy")
	}
	if err := e.SetBusinessGate(false); err != nil {
		t.Fatal(err)
	}
	if base.open || !proxy.applied || proxy.restores != 0 {
		t.Fatal("pause released protection instead of closing forwarding")
	}
}

func TestProxyApplyFailureClosesForwarding(t *testing.T) {
	base, proxy := &gateEngine{verified: true}, &testProxy{applyErr: errors.New("partial apply")}
	e := &proxyEngine{Engine: base, manager: proxy}
	if e.SetBusinessGate(true) == nil || base.open {
		t.Fatal("failed proxy transaction left forwarding open")
	}
}

func TestDisconnectRestoresProxyEvenWithoutController(t *testing.T) {
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	p := &testProxy{applied: true}
	h.proxy = p
	r := h.Handle(Request{ID: "stop", Command: "Disconnect"})
	if !r.OK || p.applied || p.restores != 1 {
		t.Fatal("disconnect left system proxy applied")
	}
}

func TestRestoreFailureCannotAcknowledgeDisconnect(t *testing.T) {
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	p := &testProxy{applied: true, restoreErr: errors.New("restore failed")}
	h.proxy = p
	r := h.Handle(Request{ID: "stop", Command: "Disconnect"})
	if r.OK || r.Snapshot["proxyRecoveryRequired"] != true {
		t.Fatal("unrestored settings were acknowledged as disconnected")
	}
}

func TestCloseRestoresOwnedProxyAndReportsFailure(t *testing.T) {
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	p := &testProxy{applied: true, restoreErr: errors.New("restore failed")}
	h.proxy = p
	if h.Close() == nil || p.restores != 1 {
		t.Fatal("cleanup concealed failed restoration")
	}
}

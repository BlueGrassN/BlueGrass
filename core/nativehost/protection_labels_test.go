package nativehost

import "testing"

type pfTunnelStub struct{ tunnelStub }

func (*pfTunnelStub) ProtectionState(forwarding bool) string {
	if forwarding {
		return "pf_forwarding"
	}
	return "pf_blocking"
}

func TestRecoveredPFTunnelReportsItsOwnProtectionBackend(t *testing.T) {
	h := NewTunnel(&pfTunnelStub{tunnelStub{active: true}})
	defer h.Close()
	r := h.Handle(Request{ID: "hello", Command: "Hello"})
	if r.Snapshot["protection"] != "pf_blocking" || r.Snapshot["protectionIntent"] != true {
		t.Fatal("recovered PF guard is not represented", r.Snapshot)
	}
}

type changedPFTunnel struct {
	*trafficSamplingEngine
	changed bool
}

func (e *changedPFTunnel) NetworkChanged() bool { return e.changed }
func (e *changedPFTunnel) ProtectionState(forwarding bool) string {
	if forwarding && !e.changed {
		return "pf_forwarding"
	}
	return "pf_blocking"
}

func TestPlatformNetworkFaultCannotRetainConnectedSnapshot(t *testing.T) {
	h, underlying := trafficSamplingHost(t)
	e := &changedPFTunnel{trafficSamplingEngine: underlying}
	h.engine, h.tunnel = e, e
	before := h.Handle(Request{ID: "before", Command: "GetStatus"})
	if before.Snapshot["connection"] != "connected" || before.Snapshot["protection"] != "pf_forwarding" {
		t.Fatal("fixture did not connect", before)
	}
	e.changed = true
	after := h.Handle(Request{ID: "after", Command: "GetStatus"})
	if after.Snapshot["connection"] != "paused" || after.Snapshot["businessGateOpen"] != false || after.Snapshot["protection"] != "pf_blocking" || after.Snapshot["errorCode"] != "NETWORK_CHANGED" || after.Snapshot["verifiedExitIpv4"] != nil {
		t.Fatal("platform failure retained stale connection", after)
	}
	if underlying.releaseCalls != 0 || !underlying.active {
		t.Fatal("network failure released protection")
	}
}

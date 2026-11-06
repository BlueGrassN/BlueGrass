package nativehost

import (
	"bluegrass/native/core/clock"
	"bluegrass/native/core/engine"
	"time"
)

type NetworkEngine interface {
	engine.Engine
	Address() string
	VerifiedIPv4() string
	Counters() (uint64, uint64)
	Close() error
}

type TunnelEngine interface {
	NetworkEngine
	Active() bool
	Release() error
	SetAllowLAN(bool) error
}

// Platform labels describe the actual guard. The legacy Windows wire values
// remain unchanged; the Darwin host reports its owned PF guard explicitly.
func (h *Host) tunnelProtectionState(forwarding bool) string {
	if backend, ok := h.tunnel.(interface{ ProtectionState(bool) string }); ok {
		value := backend.ProtectionState(forwarding)
		if forwarding && value == "pf_forwarding" || !forwarding && value == "pf_blocking" {
			return value
		}
		return "unknown"
	}
	if forwarding {
		return "wfp_forwarding"
	}
	return "wfp_blocking"
}

// NewTunnel is called only by the service after it authenticates the pipe owner.
// Close tears down forwarding but deliberately retains persistent WFP intent.
func NewTunnel(e TunnelEngine) *Host {
	epoch := randomID()
	return &Host{engine: e, tunnel: e, runtime: epoch, client: randomID(), clock: clock.New(epoch, time.Now().UTC(), clock.Local)}
}

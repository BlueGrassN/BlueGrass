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

// NewTunnel is called only by the service after it authenticates the pipe owner.
// Close tears down forwarding but deliberately retains persistent WFP intent.
func NewTunnel(e TunnelEngine) *Host {
	epoch := randomID()
	return &Host{engine: e, tunnel: e, runtime: epoch, client: randomID(), clock: clock.New(epoch, time.Now().UTC(), clock.Local)}
}

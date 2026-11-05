package nativehost

import (
	"errors"
	"sync"

	"bluegrass/native/core/engine"
)

// The engine gate and Windows settings have different lifetimes: pausing closes
// forwarding but keeps the proxy pointing at the closed listener. Only an
// explicit disconnect or process cleanup restores the previous settings.
type proxyManager interface {
	Apply(string, string) error
	Restore() error
	Relinquish() error
	State() (bool, error)
}

type proxyEngine struct {
	engine.Engine
	mu              sync.Mutex
	manager         proxyManager
	address, bypass string
}

func (e *proxyEngine) SetBusinessGate(open bool) error {
	if err := e.Engine.SetBusinessGate(open); err != nil {
		return err
	}
	if !open {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.manager.Apply(e.address, e.bypass); err != nil {
		return errors.Join(err, e.Engine.SetBusinessGate(false))
	}
	return nil
}

func proxyBypass(allowLan bool) string {
	value := "localhost;127.*;[::1]"
	if allowLan {
		value += ";<local>;*.local;*.lan;*.home.arpa;10.*;192.168.*;169.254.*"
		value += ";172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*"
	}
	return value
}

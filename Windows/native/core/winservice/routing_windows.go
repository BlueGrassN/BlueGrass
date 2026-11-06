//go:build windows

package winservice

import (
	"errors"
	"net/netip"
)

type directProtection interface {
	AllowDirect(string, netip.AddrPort) error
	RemoveDirect(string, netip.AddrPort) error
}

func (e *protectedTunnel) allowDirect(session *relaySession, network string, endpoint netip.AddrPort) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if session == nil || e.relay != session || !session.direct || !session.allowed || session.ctx.Err() != nil {
		return errors.New("STALE_TUNNEL_SESSION")
	}
	protection, ok := e.protection.(directProtection)
	if !ok {
		return errors.New("DIRECT_PROTECTION_REQUIRED")
	}
	return protection.AllowDirect(network, endpoint)
}

func (e *protectedTunnel) releaseDirect(session *relaySession, network string, endpoint netip.AddrPort) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if session == nil || e.relay != session || !session.direct || !session.allowed {
		return nil
	}
	protection, ok := e.protection.(directProtection)
	if !ok {
		return errors.New("DIRECT_PROTECTION_REQUIRED")
	}
	if err := protection.RemoveDirect(network, endpoint); err != nil {
		session.cancel()
		return errors.Join(err, e.protection.Block())
	}
	return nil
}

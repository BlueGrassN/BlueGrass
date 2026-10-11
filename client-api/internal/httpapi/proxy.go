package httpapi

import (
	"net/http"
	"net/netip"
	"strings"
)

func peerIP(remote string) netip.Addr {
	if peer, err := netip.ParseAddrPort(remote); err == nil {
		return peer.Addr().WithZone("").Unmap()
	}
	if peer, err := netip.ParseAddr(remote); err == nil {
		return peer.WithZone("").Unmap()
	}
	// Invalid peers share one bounded bucket, rather than arbitrary string keys.
	return netip.Addr{}
}

func (h *apiHandler) trustedPeer(peer netip.Addr) bool {
	if !peer.IsValid() {
		return false
	}
	for _, prefix := range h.trusted {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

func (h *apiHandler) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	values := r.Header.Values("X-Forwarded-Proto")
	return h.trustedPeer(peerIP(r.RemoteAddr)) && len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "https")
}

func (h *apiHandler) clientIP(r *http.Request) netip.Addr {
	peer := peerIP(r.RemoteAddr)
	if !h.trustedPeer(peer) {
		return peer
	}
	values := r.Header.Values("X-Real-IP")
	if len(values) == 1 {
		if real, err := netip.ParseAddr(strings.TrimSpace(values[0])); err == nil && real.Zone() == "" {
			return real.Unmap()
		}
	}
	return peer
}

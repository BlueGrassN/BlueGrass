//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"regexp"
	"strings"

	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/gate"
)

// Default control origin. The authenticated GUI can select a new HTTPS API
// using the shared bootstrap configuration; TLS identity remains pinned there.
var ControlAPIHost = "client-api.sunip.vip"

var controlHostname = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func validControlHost(host string) bool {
	if len(host) > 253 || !controlHostname.MatchString(host) {
		return false
	}
	if host == ControlAPIHost {
		return true
	}
	switch host[strings.LastIndexByte(host, '.')+1:] {
	case "local", "localhost", "internal", "invalid", "test":
		return false
	}
	return true
}

// ResolveControlHost supplies only numeric API addresses. The client must keep
// the original URI/TLS hostname and certificate validation when dialing them.
func (e *protectedTunnel) ResolveControlHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if !validControlHost(host) {
		return nil, errors.New("CONTROL_HOST_REJECTED")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("ENGINE_CLOSED")
	}
	if !e.protection.Active() {
		return nil, errors.New("PROTECTION_NOT_ACTIVE")
	}
	return e.resolveTrustedHost(ctx, host)
}

// The caller must enforce hostname syntax and the authenticated OS owner. This
// is DNS resolution only, not authority for URLs, HTTP requests or TLS identity.
// This never Arms, Blocks, Releases, or toggles a business gate. A single manager
// transaction replaces service DNS permissions before the explicit resolver runs.
func (e *protectedTunnel) resolveTrustedHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	network, err := e.network(ctx)
	if err != nil {
		return nil, errors.New("NETWORK_SNAPSHOT_FAILED")
	}
	auto := usableDNS(network.EntryDNS)
	policy := entrydns.Policy{Mode: "auto"}
	dns, err := entryPermissions(host, policy, auto)
	if err != nil {
		return nil, err
	}
	// The manager checks current ownership under the same lock as its WFP write;
	// a helper must not race a release/new owner's Arm using an earlier SID read.
	if err := e.protection.UpdateControlDNSForOwner(e.sid, dns); err != nil {
		return nil, errors.New("CONTROL_DNS_PERMISSION_FAILED")
	}
	ip, err := e.resolve(ctx, host, policy, auto)
	if err != nil || !gate.PublicIPv4(ip.String()) {
		return nil, errors.New("CONTROL_DNS_FAILED")
	}
	return []netip.Addr{ip}, nil
}

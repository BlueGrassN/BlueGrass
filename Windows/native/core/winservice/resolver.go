package winservice

import (
	"errors"
	"net/netip"

	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/winprotect"
)

// Scoped resolvers require an interface-specific exception that is not part of
// this WFP contract. Never silently fall back to another resolver or svchost.
func usableDNS(input []netip.AddrPort) []netip.AddrPort {
	var result []netip.AddrPort
	seen := map[netip.AddrPort]bool{}
	for _, endpoint := range input {
		ip := endpoint.Addr()
		if !endpoint.IsValid() || endpoint.Port() == 0 || ip.Zone() != "" || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.Is4In6() {
			continue
		}
		if !seen[endpoint] {
			seen[endpoint] = true
			result = append(result, endpoint)
		}
	}
	return result
}

// Literal entries need no DNS permission, even when no automatic candidates
// exist. Hostnames use exactly the same validated, ordered candidates as Resolve.
func entryPermissions(host string, policy entrydns.Policy, auto []netip.AddrPort) ([]winprotect.DNSEndpoint, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, nil
	}
	candidates, err := policy.Effective(auto)
	if err != nil {
		return nil, err
	}
	result := make([]winprotect.DNSEndpoint, 0, len(candidates))
	for _, candidate := range candidates {
		ep := candidate.Endpoint()
		if len(usableDNS([]netip.AddrPort{ep})) != 1 {
			return nil, errors.New("INVALID_ENTRY_DNS")
		}
		result = append(result, winprotect.DNSEndpoint{Endpoint: ep, Transport: candidate.Transport})
	}
	return uniqueDNSPermissions(result), nil
}

func uniqueDNSPermissions(input []winprotect.DNSEndpoint) []winprotect.DNSEndpoint {
	seen := make(map[winprotect.DNSEndpoint]bool)
	var result []winprotect.DNSEndpoint
	for _, endpoint := range input {
		if !seen[endpoint] {
			seen[endpoint] = true
			result = append(result, endpoint)
		}
	}
	return result
}

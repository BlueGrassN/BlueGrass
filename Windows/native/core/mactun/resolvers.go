package mactun

import (
	"errors"
	"net/netip"
)

type resolverInterface struct {
	Up, Hardware bool
	Addresses    []netip.Prefix
}

// Up + a MAC also describes enabled, unassociated Wi-Fi. Only links with a
// usable IPv4 source participate in this IPv4 takeover; skip others before
// inspecting DNS/DHCP. A participating link's unverifiable DNS remains an error.
func collectResolvers(services []Resolver, u Underlay, inspect func(string) (resolverInterface, error), load func(Resolver) (Resolver, error)) ([]Resolver, error) {
	var result []Resolver
	defaultFound := false
	for _, service := range services {
		if !interfacePattern.MatchString(service.Interface) {
			continue
		}
		info, err := inspect(service.Interface)
		usable := false
		defaultSource := false
		if err == nil && info.Up && info.Hardware {
			for _, address := range info.Addresses {
				ip := address.Addr()
				if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.Is4In6() || netip.MustParsePrefix("198.18.0.0/15").Contains(ip) {
					continue
				}
				usable = true
				defaultSource = defaultSource || ip == u.Source
			}
		}
		if !usable || service.Interface == u.Interface && !defaultSource {
			continue
		}
		resolver, err := load(service)
		if err != nil {
			return nil, err
		}
		if !validAddresses(resolver.Effective) || len(resolver.Effective) == 0 {
			return nil, errors.New("PHYSICAL_DNS_UNAVAILABLE")
		}
		result = append(result, resolver)
		defaultFound = defaultFound || service.Interface == u.Interface
	}
	if !defaultFound {
		return nil, errors.New("PHYSICAL_SERVICE_UNAVAILABLE")
	}
	return result, nil
}

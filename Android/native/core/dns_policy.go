package bridge

import "net/netip"

func dnsConnectedResolver(info networkInfo) string {
	if info.Name == "" || info.Index < 1 {
		return ""
	}
	for _, value := range info.DNSServers {
		address, err := netip.ParseAddr(value)
		if err != nil || (address.Zone() != "" && address.Zone() != info.Name) {
			continue
		}
		address = address.WithZone("")
		if !dnsPhysicalUnicast(address) {
			continue
		}
		// Only address-derived connected prefixes prove an on-link resolver.
		// Routes lack gateway/ownership metadata; a private static/default route
		// alone is not sufficient evidence of a trusted local resolver.
		for _, value := range info.Addresses {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || !dnsPhysicalUnicast(prefix.Addr()) || !dnsPrefixHost(prefix, address) {
				continue
			}
			for _, scope := range privateRanges {
				if prefix.Bits() >= scope.Bits() && scope.Contains(prefix.Masked().Addr()) {
					if address.Is6() && address.IsLinkLocalUnicast() {
						address = address.WithZone(info.Name)
					}
					return address.String()
				}
			}
		}
	}
	return ""
}

func mdnsPhysicalIPv4(info networkInfo) (netip.Prefix, bool) {
	if info.Name != "" && info.Index > 0 {
		for _, value := range info.Addresses {
			prefix, err := netip.ParsePrefix(value)
			if err == nil && prefix.Addr().Is4() && dnsPhysicalUnicast(prefix.Addr()) && dnsPrefixHost(prefix, prefix.Addr()) {
				return prefix, true
			}
		}
	}
	return netip.Prefix{}, false
}

var dnsExcludedPhysicalRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("172.19.0.0/30"),
	netip.MustParsePrefix("fdfe:dcba:9876::/126"),
}

func dnsPhysicalUnicast(address netip.Addr) bool {
	if !address.IsValid() || address.Is4In6() || address.IsLoopback() || (!address.IsGlobalUnicast() && !address.IsLinkLocalUnicast()) {
		return false
	}
	for _, prefix := range dnsExcludedPhysicalRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func dnsPrefixHost(prefix netip.Prefix, address netip.Addr) bool {
	if prefix.Bits() <= 0 || !prefix.Contains(address) {
		return false
	}
	if address.Is4() && prefix.Bits() < 31 {
		if address == prefix.Masked().Addr() {
			return false
		}
		bytes := address.As4()
		n := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
		hostMask := uint32(1)<<(32-prefix.Bits()) - 1
		if n&hostMask == hostMask {
			return false
		}
	}
	return true
}

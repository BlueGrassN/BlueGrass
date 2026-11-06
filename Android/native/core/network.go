package bridge

import (
	"encoding/json"
	"net/netip"
)

type networkInfo struct {
	Name       string   `json:"name"`
	Index      int      `json:"index"`
	MTU        int      `json:"mtu"`
	Addresses  []string `json:"addresses"`
	DNSServers []string `json:"dnsServers"`
	Domains    []string `json:"domains"`
	Routes     []string `json:"routes,omitempty"`
}

func parseNetwork(raw string) (networkInfo, error) {
	var info networkInfo
	if len(raw) > 32768 || json.Unmarshal([]byte(raw), &info) != nil || info.Name == "" || len(info.Name) > 64 || info.Index < 1 || info.MTU < 576 || info.MTU > 65535 || len(info.Addresses) > 64 || len(info.Routes) > 128 || len(info.Domains) > 32 || len(info.DNSServers) > 16 {
		return info, errNetwork
	}
	for _, address := range info.Addresses {
		if _, err := netip.ParsePrefix(address); err != nil {
			return networkInfo{}, errNetwork
		}
	}
	for _, server := range info.DNSServers {
		if ip, err := netip.ParseAddr(server); err != nil || ip.IsUnspecified() || ip.IsMulticast() {
			return networkInfo{}, errNetwork
		}
	}
	return info, nil
}

var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
}

func localRanges(info networkInfo) []netip.Prefix {
	var result []netip.Prefix
	seen := map[string]bool{}
	add := func(p netip.Prefix) {
		p = p.Masked()
		if !seen[p.String()] {
			seen[p.String()] = true
			result = append(result, p)
		}
	}
	for _, s := range info.Addresses {
		p, e := netip.ParsePrefix(s)
		if e == nil && p.Bits() > 0 && !p.Addr().IsUnspecified() && !p.Addr().IsMulticast() && !p.Addr().IsLoopback() {
			add(p)
		}
	}
	for _, s := range info.Routes {
		p, e := netip.ParsePrefix(s)
		if e != nil || p.Bits() == 0 {
			continue
		}
		for _, scope := range privateRanges {
			if p.Bits() >= scope.Bits() && scope.Contains(p.Addr()) {
				add(p)
				break
			}
		}
	}
	return result
}

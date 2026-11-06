package tunengine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// LocalOnlyConfig is service-owned policy, never a connection profile or raw IPC
// config. Network is the same trusted snapshot used to authorize local DNS in
// WFP. CurrentNetwork must return promptly on cancellation; a different generation
// retires this instance. A new instance and fresh WFP transaction are then needed.
type LocalOnlyConfig struct {
	AllowLAN       bool // false explicitly keeps only localhost/loopback
	Network        NetworkSnapshot
	CurrentNetwork func(context.Context) (NetworkSnapshot, error)
	// Retained as a validated trusted installation field. It grants no route:
	// original GUI/helper sockets must bind their own physical control path.
	UIExecutable string
}

type localOnlyPrepared struct {
	network    NetworkSnapshot
	allowLAN   bool
	ui         string
	tun4, tun6 netip.Prefix
}

func localOnlyVirtualPrefixes() []netip.Prefix {
	var result []netip.Prefix
	for _, value := range []string{"198.18.0.0/15", "fc00::/18", "172.19.0.0/30", "172.31.255.0/30", "172.30.255.0/30", "10.255.255.0/30", "fdfe:dcba:9876::/126", "fdde:adbe:ef01::/126"} {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func prepareLocalOnly(c LocalOnlyConfig) (localOnlyPrepared, error) {
	n := c.Network
	if n.Generation == 0 || len(n.Routes) > 256 || len(n.DNS) > 32 || len(n.Occupied) > 4096 {
		return localOnlyPrepared{}, errors.New("INVALID_LOCAL_ONLY_NETWORK")
	}
	n.Routes = append([]LocalRoute(nil), n.Routes...)
	n.DNS = append([]LocalDNS(nil), n.DNS...)
	n.Occupied = append([]netip.Prefix(nil), n.Occupied...)
	n.EntryDNS = nil // Never an upstream for this runtime.
	occupied := append([]netip.Prefix(nil), n.Occupied...)
	for _, p := range occupied {
		if !p.IsValid() || p.Bits() == 0 {
			return localOnlyPrepared{}, errors.New("INVALID_OCCUPIED_PREFIX")
		}
	}
	for _, r := range n.Routes {
		p := r.Prefix
		if !p.IsValid() || p != p.Masked() || p.Bits() == 0 || p.Addr().Is4In6() || !validInterface(r.Interface) || !(within(p, localUnicast) || r.Scoped && within(p, localScoped) || r.Connected && p.Addr().IsGlobalUnicast()) {
			return localOnlyPrepared{}, errors.New("INVALID_LOCAL_ROUTE")
		}
		occupied = append(occupied, p)
	}
	sort.SliceStable(n.Routes, func(i, j int) bool {
		a, b := n.Routes[i], n.Routes[j]
		if a.Prefix.Bits() != b.Prefix.Bits() {
			return a.Prefix.Bits() > b.Prefix.Bits()
		}
		return a.Metric < b.Metric
	})
	for i, a := range n.Routes {
		for _, b := range n.Routes[i+1:] {
			if ambiguousScope(a, b) || a.Prefix == b.Prefix && a.Metric == b.Metric && a.Interface != b.Interface {
				return localOnlyPrepared{}, errors.New("AMBIGUOUS_LOCAL_ROUTE")
			}
		}
	}
	domains := map[string]bool{}
	for i := range n.DNS {
		d := &n.DNS[i]
		d.Domains = append([]string(nil), d.Domains...)
		if !d.Server.IsValid() || d.Server.Port() != 53 || d.Server.Addr().Is4In6() || !validInterface(d.Interface) || len(d.Domains) > 32 {
			return localOnlyPrepared{}, errors.New("INVALID_LOCAL_DNS")
		}
		server := d.Server.Addr().WithZone("")
		for _, pool := range localOnlyVirtualPrefixes() {
			if pool.Contains(server) {
				return localOnlyPrepared{}, errors.New("VIRTUAL_LOCAL_DNS")
			}
		}
		if server.IsLoopback() || server.IsUnspecified() || server.IsMulticast() {
			return localOnlyPrepared{}, errors.New("INVALID_LOCAL_DNS")
		}
		if zone := d.Server.Addr().Zone(); zone != "" && zone != d.Interface {
			return localOnlyPrepared{}, errors.New("INVALID_LOCAL_DNS_SCOPE")
		}
		reachable := false
		for _, r := range n.Routes {
			if r.Interface == d.Interface && r.Prefix.Contains(server) && (within(r.Prefix, localUnicast) || r.Connected) {
				reachable = true
				break
			}
		}
		if !reachable {
			return localOnlyPrepared{}, errors.New("LOCAL_DNS_ROUTE_REQUIRED")
		}
		for j, name := range d.Domains {
			name = normalizedName(name)
			if !validLocalOnlyName(name) || name == "local" || strings.HasSuffix(name, ".local") || name == "localhost" || strings.HasSuffix(name, ".localhost") || !strings.Contains(name, ".") && name != "lan" || domains[name] {
				return localOnlyPrepared{}, errors.New("INVALID_LOCAL_DNS_DOMAIN")
			}
			for old := range domains {
				if strings.HasSuffix(name, "."+old) || strings.HasSuffix(old, "."+name) {
					return localOnlyPrepared{}, errors.New("AMBIGUOUS_LOCAL_DNS_DOMAIN")
				}
			}
			d.Domains[j] = name
			domains[name] = true
		}
	}
	if n.MDNSInterface != "" {
		known := false
		for _, r := range n.Routes {
			if r.Interface == n.MDNSInterface {
				known = true
			}
		}
		if !known || !validInterface(n.MDNSInterface) {
			return localOnlyPrepared{}, errors.New("INVALID_MDNS_INTERFACE")
		}
	}
	tun4, err := choosePool([]string{"172.31.255.1/30", "172.30.255.1/30", "10.255.255.1/30"}, occupied)
	if err != nil {
		return localOnlyPrepared{}, err
	}
	tun6, err := choosePool([]string{"fdfe:dcba:9876::1/126", "fdde:adbe:ef01::1/126"}, occupied)
	if err != nil {
		return localOnlyPrepared{}, err
	}
	ui := c.UIExecutable
	if ui == "" {
		ui = UIExecutable
	}
	if len(ui) < 4 || ui[1:3] != `:\` || strings.ContainsAny(ui, "\x00\r\n*?\"") || strings.Contains(ui, `\..\`) || !strings.HasSuffix(strings.ToLower(ui), ".exe") {
		return localOnlyPrepared{}, errors.New("INVALID_TRUSTED_UI_PATH")
	}
	return localOnlyPrepared{network: n, allowLAN: c.AllowLAN, ui: ui, tun4: tun4, tun6: tun6}, nil
}

func validLocalOnlyName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

func localOnlyConfigObject(p localOnlyPrepared) obj {
	virtual := []string{}
	for _, prefix := range localOnlyVirtualPrefixes() {
		virtual = append(virtual, prefix.String())
	}
	rules := []obj{
		{"port": []int{53}, "action": "hijack-dns"},
		{"action": "resolve", "server": "local-only", "strategy": "prefer_ipv4"},
		{"ip_cidr": virtual, "action": "reject"},
		{"ip_cidr": []string{"127.0.0.0/8", "::1/128"}, "outbound": "local-loopback"},
	}
	outs := []obj{{"type": "direct", "tag": "local-loopback", "inet4_bind_address": "127.0.0.1", "inet6_bind_address": "::1"}}
	if p.allowLAN {
		for i, r := range p.network.Routes {
			tag := fmt.Sprintf("local-lan-%d", i)
			outs = append(outs, obj{"type": "direct", "tag": tag, "bind_interface": r.Interface})
			rules = append(rules, obj{"ip_cidr": []string{r.Prefix.String()}, "outbound": tag})
		}
	}
	rules = append(rules, obj{"action": "reject"})
	return obj{"log": obj{"disabled": true},
		"outbounds": outs, "route": obj{"auto_detect_interface": true, "find_process": true, "default_domain_resolver": "local-only", "rules": rules},
		"dns": obj{"servers": []obj{{"type": "bluegrass-local-only", "tag": "local-only"}}, "final": "local-only", "disable_cache": true},
	}
}

func decodeLocalOnlyOptions(p localOnlyPrepared, _ *sessionGate, _ func(context.Context) bool) (nativeConfig, error) {
	return decodePolicy(localOnlyConfigObject(p))
}

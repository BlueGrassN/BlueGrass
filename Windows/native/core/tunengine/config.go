package tunengine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"github.com/xtls/xray-core/infra/conf"
)

const (
	// A release installation constant, never an IPC/profile-controlled bypass.
	UIExecutable    = `C:\Program Files\BlueGrass\bluegrass.exe`
	PublicDNSServer = "1.1.1.1"
)

// Config is trusted service configuration. nil AllowLAN means the ADR0031
// default (true). Changing any policy requires Stop and a fresh Prepare.
type Config struct {
	AllowLAN *bool
	// Absolute installed image path from trusted service configuration, not IPC.
	UIExecutable string
	Network      func(context.Context) (NetworkSnapshot, error)
	// Trusted absolute service-owned database path, NEVER profile/IPC input.
	// Empty explicitly uses volatile mappings. Parent must already have a
	// protected SYSTEM/Administrators-only ACL. Stop/Close preserve the cache;
	// the host calls ClearCache after explicit disconnect/logout/release.
	CachePath string
	// Authorizes an authenticated SOCKS5 UDP ASSOCIATE BND endpoint before any
	// UDP socket is created. nil denies UDP. Called only with a literal IPv4
	// from the authenticated relay reply, while its current business gate is open.
	// The host MUST bind this closure to the current session, serialize it with
	// WFP Block, bound rule lifetime, and clear permissions on Block. It must
	// return promptly; no engine locks are held and callback errors are sanitized.
	AllowUDPRelay func(netip.AddrPort) error
	// Releases a successful association's endpoint permission after its socket
	// closes. The host binds it to the same session as AllowUDPRelay.
	ReleaseUDPRelay func(netip.AddrPort) error
	// Precise physical egress permits for trusted split-routing decisions.
	AllowDirect   func(string, netip.AddrPort) error
	ReleaseDirect func(string, netip.AddrPort) error
}

// NetworkSnapshot must describe the underlying host before this product's TUN
// routes. Generation changes on interface, route, resolver or policy changes.
// Routes are selected by longest prefix then Metric, never by the default NIC.
// Occupied includes interface addresses AND configured routes, excluding /0.
type NetworkSnapshot struct {
	Generation    uint64
	Routes        []LocalRoute
	DNS           []LocalDNS
	EntryDNS      []netip.AddrPort // physical default-network bootstrap candidates; independent of local-name DNS
	MDNSInterface string           // exactly one known link; no cross-interface fanout
	Occupied      []netip.Prefix
}
type LocalRoute struct {
	Prefix    netip.Prefix
	Interface string
	Metric    uint32
	Connected bool // permits an actual on-link prefix, including local global IPv6
	Scoped    bool // explicit original link/scope for broadcast or multicast
}
type LocalDNS struct {
	Server    netip.AddrPort
	Interface string
	Domains   []string // verified DNS routing domains; e.g. lan and home.arpa
}

type preparedConfig struct {
	selected          *conf.OutboundDetourConfig
	protocol          protocolconfig.Config
	profile           engine.Profile
	entry             netip.AddrPort
	network           NetworkSnapshot
	allowLAN          bool
	uiExecutable      string
	allowUDPRelay     func(netip.AddrPort) error
	releaseUDPRelay   func(netip.AddrPort) error
	allowDirect       func(string, netip.AddrPort) error
	releaseDirect     func(string, netip.AddrPort) error
	routingMode       routingpolicy.Mode
	cachePath         string
	tun4, tun6, fake4 netip.Prefix
}

var localUnicast = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
}
var localScoped = []netip.Prefix{
	netip.MustParsePrefix("224.0.0.0/24"), netip.MustParsePrefix("239.0.0.0/8"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("ff01::/16"), netip.MustParsePrefix("ff02::/16"),
	netip.MustParsePrefix("ff04::/16"), netip.MustParsePrefix("ff05::/16"), netip.MustParsePrefix("ff08::/16"),
}

func within(p netip.Prefix, ranges []netip.Prefix) bool {
	for _, r := range ranges {
		if r.Contains(p.Addr()) && p.Bits() >= r.Bits() {
			return true
		}
	}
	return false
}
func validInterface(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.EqualFold(name, InterfaceName) && !strings.ContainsAny(name, "\x00\r\n")
}
func ambiguousScope(a, b LocalRoute) bool {
	return a.Interface != b.Interface && a.Prefix.Overlaps(b.Prefix) && (a.Scoped || b.Scoped || a.Prefix.Addr().IsLinkLocalUnicast() || b.Prefix.Addr().IsLinkLocalUnicast())
}
func domainPattern(name string) string  { return `(?i)(^|\.)` + regexp.QuoteMeta(name) + `\.?$` }
func normalizedName(name string) string { return strings.TrimSuffix(strings.ToLower(name), ".") }
func localName(name string) bool {
	name = normalizedName(name)
	for _, suffix := range []string{"lan", "home.arpa", "local", "localhost"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return true
		}
	}
	return false
}

// Reverse zones are bounded by the represented address prefix, including
// non-octet IPv4 and non-nibble IPv6 prefixes. No whole arpa tree is exempted.
func reversePatterns(prefix netip.Prefix) []string {
	step, suffix := 4, "ip6.arpa"
	if prefix.Addr().Is4() {
		step, suffix = 8, "in-addr.arpa"
	}
	bits := (prefix.Bits() + step - 1) / step * step
	count := 1 << (bits - prefix.Bits())
	var result []string
	for i := 0; i < count; i++ {
		var parts []string
		if step == 8 {
			b := prefix.Masked().Addr().As4()
			b[bits/8-1] += byte(i)
			for j := bits/8 - 1; j >= 0; j-- {
				parts = append(parts, strconv.Itoa(int(b[j])))
			}
		} else {
			b := prefix.Masked().Addr().As16()
			index := (bits - 1) / 8
			if bits%8 == 4 {
				b[index] += byte(i << 4)
			} else {
				b[index] += byte(i)
			}
			for j := bits/4 - 1; j >= 0; j-- {
				v := b[j/2]
				if j%2 == 0 {
					v >>= 4
				}
				parts = append(parts, strconv.FormatUint(uint64(v&15), 16))
			}
		}
		result = append(result, domainPattern(strings.Join(parts, ".")+"."+suffix))
	}
	return result
}
func choosePool(candidates []string, occupied []netip.Prefix) (netip.Prefix, error) {
	for _, value := range candidates {
		p := netip.MustParsePrefix(value)
		conflict := false
		for _, other := range occupied {
			if p.Overlaps(other) {
				conflict = true
				break
			}
		}
		if !conflict {
			return p, nil
		}
	}
	return netip.Prefix{}, errors.New("TUN_ADDRESS_CONFLICT")
}
func prepareConfig(p engine.Profile, c Config, n NetworkSnapshot, entry netip.Addr) (preparedConfig, error) {
	return prepareProtocolConfig(p, c, n, entry, protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: p.Auth.Username, Password: p.Auth.Password}})
}

func prepareProtocolConfig(p engine.Profile, c Config, n NetworkSnapshot, entry netip.Addr, protocol protocolconfig.Config) (preparedConfig, error) {
	routingMode, routingErr := routingpolicy.Normalize(p.RoutingMode)
	if routingErr != nil {
		return preparedConfig{}, routingErr
	}
	if c.CachePath != "" && (!validCachePath(c.CachePath) || p.Key.Environment == "" || p.Key.DataSource == "" || p.Key.AccountKey == "" || p.Key.Kind == "" || p.Key.SourceID == "") {
		return preparedConfig{}, errors.New("INVALID_CACHE_CONFIGURATION")
	}
	if p.Protocol != protocol.Type || p.Mode != "tun" {
		return preparedConfig{}, errors.New("UNSUPPORTED_PROFILE")
	}
	if !entry.Is4() || !gate.PublicIPv4(entry.String()) || p.Entry.Port < 1 || p.Entry.Port > 65535 {
		return preparedConfig{}, errors.New("INVALID_ENTRY")
	}
	selected, err := protocolconfig.Build(protocol, netip.AddrPortFrom(entry, uint16(p.Entry.Port)))
	if err != nil {
		return preparedConfig{}, err
	}
	if n.Generation == 0 {
		return preparedConfig{}, errors.New("NETWORK_GENERATION_REQUIRED")
	}
	// Detach all slices from the provider; concurrent host refresh cannot mutate
	// a prepared routing or DNS policy behind the verification boundary.
	n.Routes = append([]LocalRoute(nil), n.Routes...)
	n.Occupied = append([]netip.Prefix(nil), n.Occupied...)
	n.DNS = append([]LocalDNS(nil), n.DNS...)
	n.EntryDNS = append([]netip.AddrPort(nil), n.EntryDNS...)
	occupied := append([]netip.Prefix(nil), n.Occupied...)
	for _, p := range occupied {
		if !p.IsValid() || p.Bits() == 0 {
			return preparedConfig{}, errors.New("INVALID_OCCUPIED_PREFIX")
		}
	}
	for _, route := range n.Routes {
		p := route.Prefix
		if !p.IsValid() || p != p.Masked() || p.Bits() == 0 || !validInterface(route.Interface) || p.Addr().Is4In6() {
			return preparedConfig{}, errors.New("INVALID_LOCAL_ROUTE")
		}
		if !(within(p, localUnicast) || route.Scoped && within(p, localScoped) || route.Connected && p.Addr().IsGlobalUnicast()) {
			return preparedConfig{}, errors.New("INVALID_LOCAL_SCOPE")
		}
		occupied = append(occupied, p)
	}
	sort.SliceStable(n.Routes, func(i, j int) bool {
		if n.Routes[i].Prefix.Bits() != n.Routes[j].Prefix.Bits() {
			return n.Routes[i].Prefix.Bits() > n.Routes[j].Prefix.Bits()
		}
		return n.Routes[i].Metric < n.Routes[j].Metric
	})
	for i := 1; i < len(n.Routes); i++ {
		a, b := n.Routes[i-1], n.Routes[i]
		if a.Prefix == b.Prefix && a.Metric == b.Metric && a.Interface != b.Interface {
			return preparedConfig{}, errors.New("AMBIGUOUS_LOCAL_ROUTE")
		}
	}
	for i, a := range n.Routes {
		for _, b := range n.Routes[i+1:] {
			if ambiguousScope(a, b) {
				return preparedConfig{}, errors.New("AMBIGUOUS_LOCAL_SCOPE")
			}
		}
	}
	domains := map[string]bool{}
	for i := range n.DNS {
		d := &n.DNS[i]
		d.Domains = append([]string(nil), d.Domains...)
		if !d.Server.IsValid() || d.Server.Port() == 0 || !validInterface(d.Interface) {
			return preparedConfig{}, errors.New("INVALID_LOCAL_DNS")
		}
		reachable := false
		for _, r := range n.Routes {
			if r.Prefix.Contains(d.Server.Addr().WithZone("")) && r.Interface == d.Interface && (within(r.Prefix, localUnicast) || r.Connected) {
				reachable = true
				break
			}
		}
		if !reachable {
			return preparedConfig{}, errors.New("LOCAL_DNS_ROUTE_REQUIRED")
		}
		for j, name := range d.Domains {
			name = normalizedName(name)
			if name == "" || strings.ContainsAny(name, "* /\\:\x00") || name == "local" || name == "localhost" || !strings.Contains(name, ".") && name != "lan" || domains[name] {
				return preparedConfig{}, errors.New("INVALID_LOCAL_DNS_DOMAIN")
			}
			for _, label := range strings.Split(name, ".") {
				if label == "" {
					return preparedConfig{}, errors.New("INVALID_LOCAL_DNS_DOMAIN")
				}
			}
			domains[name] = true
			d.Domains[j] = name
		}
	}
	if n.MDNSInterface != "" && !validInterface(n.MDNSInterface) {
		return preparedConfig{}, errors.New("INVALID_MDNS_INTERFACE")
	}
	tun4, err := choosePool([]string{"172.31.255.1/30", "172.30.255.1/30", "10.255.255.1/30"}, occupied)
	if err != nil {
		return preparedConfig{}, err
	}
	tun6, err := choosePool([]string{"fdfe:dcba:9876::1/126", "fdde:adbe:ef01::1/126"}, occupied)
	if err != nil {
		return preparedConfig{}, err
	}
	fake4, err := choosePool([]string{"198.18.0.0/16", "198.19.0.0/16"}, occupied)
	if err != nil {
		return preparedConfig{}, err
	}
	ui := c.UIExecutable
	if ui == "" {
		ui = UIExecutable
	}
	if len(ui) < 4 || ui[1:3] != `:\` || strings.ContainsAny(ui, "\x00\r\n*?\"") || strings.Contains(ui, `\..\`) || !strings.HasSuffix(strings.ToLower(ui), ".exe") {
		return preparedConfig{}, errors.New("INVALID_TRUSTED_UI_PATH")
	}
	return preparedConfig{selected: selected, protocol: protocol, profile: p, entry: netip.AddrPortFrom(entry, uint16(p.Entry.Port)), network: n,
		allowLAN: c.AllowLAN == nil || *c.AllowLAN, uiExecutable: ui, allowUDPRelay: c.AllowUDPRelay, releaseUDPRelay: c.ReleaseUDPRelay,
		allowDirect: c.AllowDirect, releaseDirect: c.ReleaseDirect, routingMode: routingMode,
		cachePath: c.CachePath, tun4: tun4, tun6: tun6, fake4: fake4}, nil
}

type obj = map[string]any

// Options are constructed here, never accepted as arbitrary engine JSON.
func configObject(p preparedConfig) obj {
	rules := []obj{
		{"port": []int{53}, "action": "hijack-dns"},
		{"action": "resolve", "server": "public", "strategy": "ipv4_only"},
		{"ip_cidr": []string{p.tun4.Masked().String(), p.tun6.Masked().String(), "198.18.0.0/15"}, "action": "reject"},
		{"ip_cidr": []string{"127.0.0.0/8", "::1/128"}, "outbound": "loopback"},
	}
	outs := []obj{
		{"type": "direct", "tag": "loopback", "inet4_bind_address": "127.0.0.1", "inet6_bind_address": "::1"},
		{"type": "direct", "tag": "control"},
	}
	// Derive the exact installed sibling; no profile/IPC updater path is accepted.
	// The host separately verifies its publisher and authorizes it in WFP, while
	// the signed helper enforces HTTPS hostname allowlisting and certificate trust.
	updater := p.uiExecutable[:strings.LastIndex(p.uiExecutable, `\`)+1] + "BlueGrass.Update.exe"
	rules = append(rules, obj{"process_path": []string{p.uiExecutable, updater}, "network": "tcp", "port": []int{443}, "ip_version": 4, "outbound": "control"})
	if p.allowLAN {
		for i, r := range p.network.Routes {
			tag := fmt.Sprintf("lan-%d", i)
			outs = append(outs, obj{"type": "direct", "tag": tag, "bind_interface": r.Interface})
			rules = append(rules, obj{"ip_cidr": []string{r.Prefix.String()}, "outbound": tag})
		}
	}
	blocked := []string{"0.0.0.0/8", "224.0.0.0/4", "240.0.0.0/4", "198.18.0.0/15"}
	for _, r := range localUnicast {
		blocked = append(blocked, r.String())
	}
	rules = append(rules, obj{"ip_cidr": blocked, "action": "reject"}, obj{"ip_version": 6, "action": "reject"})
	servers := []obj{
		{"type": "tcp", "tag": "public", "server": PublicDNSServer, "server_port": 53, "detour": "selected"},
		{"type": "fakeip", "tag": "fakeip", "inet4_range": p.fake4.String()},
	}
	dnsRules := []obj{
		{"domain_regex": []string{domainPattern("localhost")}, "query_type": []string{"A"}, "action": "predefined", "rcode": "NOERROR", "answer": []string{"*. 60 IN A 127.0.0.1"}},
		{"domain_regex": []string{domainPattern("localhost")}, "query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR", "answer": []string{"*. 60 IN AAAA ::1"}},
		{"domain_regex": []string{domainPattern("localhost")}, "action": "predefined", "rcode": "NOERROR"},
	}
	localPatterns := []string{domainPattern("lan"), domainPattern("home.arpa"), domainPattern("local")}
	for _, prefix := range append(append([]netip.Prefix(nil), localUnicast...), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("198.18.0.0/15")) {
		localPatterns = append(localPatterns, reversePatterns(prefix)...)
	}
	for i, d := range p.network.DNS {
		patterns := []string{}
		for _, name := range d.Domains {
			patterns = append(patterns, domainPattern(name))
			localPatterns = append(localPatterns, domainPattern(name))
		}
		for _, route := range p.network.Routes {
			if route.Interface == d.Interface && !route.Prefix.Addr().IsLinkLocalUnicast() && !route.Prefix.Addr().IsMulticast() {
				patterns = append(patterns, reversePatterns(route.Prefix)...)
			}
		}
		if p.allowLAN && len(patterns) > 0 {
			tag := fmt.Sprintf("local-dns-%d", i)
			servers = append(servers, obj{"type": "tcp", "tag": tag, "server": d.Server.Addr().String(), "server_port": d.Server.Port(), "bind_interface": d.Interface})
			dnsRules = append(dnsRules, obj{"domain_regex": patterns, "server": tag, "disable_cache": true})
		}
	}
	if p.allowLAN && p.network.MDNSInterface != "" {
		servers = append(servers, obj{"type": "mdns", "tag": "mdns", "interface": []string{p.network.MDNSInterface}})
		patterns := []string{domainPattern("local")}
		patterns = append(patterns, reversePatterns(netip.MustParsePrefix("169.254.0.0/16"))...)
		patterns = append(patterns, reversePatterns(netip.MustParsePrefix("fe80::/10"))...)
		dnsRules = append(dnsRules, obj{"domain_regex": patterns, "server": "mdns", "disable_cache": true})
	}
	// Unknown/unavailable local resolvers NEVER fall through to a public server.
	dnsRules = append(dnsRules,
		obj{"domain_regex": localPatterns, "action": "predefined", "rcode": "SERVFAIL"},
		obj{"query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR"},
		obj{"query_type": []string{"A"}, "server": "fakeip"})
	return obj{
		"log":       obj{"disabled": true},
		"outbounds": outs,
		"route":     obj{"auto_detect_interface": true, "find_process": true, "default_domain_resolver": "public", "final": "selected", "rules": rules},
		"dns":       obj{"servers": servers, "rules": dnsRules, "final": "public", "independent_cache": true},
	}
}

func decodeOptions(p preparedConfig, _ *sessionGate) (nativeConfig, error) {
	return decodePolicy(configObject(p))
}

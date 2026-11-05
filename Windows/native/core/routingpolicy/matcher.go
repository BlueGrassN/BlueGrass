// Package routingpolicy classifies destinations using bundled Loyalsoldier data
// and the existing Xray core's domain matcher. Bundled classification performs
// no network or OS I/O.
package routingpolicy

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"bluegrass/native/core/gate"
	"github.com/xtls/xray-core/common/geodata"
	"golang.org/x/net/idna"
	"google.golang.org/protobuf/proto"
)

//go:embed assets
var bundled embed.FS

// Decision is a domain classification; Unknown defers to the resolved IP.
type Decision uint8

const (
	Unknown Decision = iota
	Direct
	Proxy
)

// Rules is immutable after loading and safe for concurrent lookup. Its zero
// value (and a nil *Rules) never authorizes direct routing.
type Rules struct {
	domestic geodata.DomainMatcher
	foreign  geodata.DomainMatcher
	cn       []netip.Prefix // Sorted by starting address, with no overlaps.
}

var loadOnce = sync.OnceValues(func() (*Rules, error) {
	site, err := bundled.ReadFile("assets/geosite-cn-foreign.dat")
	if err != nil {
		return nil, err
	}
	ip, err := bundled.ReadFile("assets/geoip-cn-v4.dat")
	if err != nil {
		return nil, err
	}
	return loadBundled(site, ip)
})

// Load verifies both bundled SHA-256 digests before decoding and building the
// matchers. All callers share the same immutable result, including load errors.
func Load() (*Rules, error) { return loadOnce() }

// Domain checks the known foreign set first, then CN. Input is a hostname, not
// a URL or host:port. Case, one DNS root dot and IDNA names are normalized;
// malformed hostnames and IP literals return Unknown.
func (r *Rules) Domain(host string) Decision {
	if r == nil || r.domestic == nil || r.foreign == nil {
		return Unknown
	}
	host = normalizeDomain(host)
	if host == "" {
		return Unknown
	}
	if r.foreign.MatchAny(host) {
		return Proxy
	}
	if r.domestic.MatchAny(host) {
		return Direct
	}
	return Unknown
}

// IsCN accepts only native public IPv4 allowed by gate.PublicIPv4. Mapped IPv6,
// private, CGNAT, FakeIP, documentation and other special ranges cannot pass.
func (r *Rules) IsCN(ip netip.Addr) bool {
	if r == nil || !gate.PublicIPv4(ip.String()) {
		return false
	}
	i := sort.Search(len(r.cn), func(i int) bool { return r.cn[i].Addr().Compare(ip) > 0 })
	return i > 0 && r.cn[i-1].Contains(ip)
}

// ShouldDirect applies domain priority to an already resolved public IPv4.
// A CN domain permits any public IPv4; a foreign domain always proxies. Only an
// unknown domain (including an absent domain) falls back to CN IP membership.
// The caller must dial this exact IP and must separately enforce OS permissions.
func (r *Rules) ShouldDirect(domain string, ip netip.Addr) bool {
	if r == nil || !gate.PublicIPv4(ip.String()) {
		return false
	}
	switch r.Domain(domain) {
	case Proxy:
		return false
	case Direct:
		return true
	default:
		return r.IsCN(ip)
	}
}

func normalizeDomain(host string) string {
	if len(host) > 1024 {
		return ""
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || len(ascii) == 0 || len(ascii) > 253 {
		return ""
	}
	if _, err := netip.ParseAddr(ascii); err == nil {
		return ""
	}
	for _, label := range strings.Split(ascii, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return ""
			}
		}
	}
	return ascii
}

func loadBundled(site, ip []byte) (*Rules, error) {
	if fmt.Sprintf("%x", sha256.Sum256(site)) != geositeSHA256 {
		return nil, fmt.Errorf("routing geosite SHA-256 mismatch")
	}
	if fmt.Sprintf("%x", sha256.Sum256(ip)) != geoipSHA256 {
		return nil, fmt.Errorf("routing geoip SHA-256 mismatch")
	}
	return decodeRules(site, ip)
}

func decodeRules(siteData, ipData []byte) (*Rules, error) {
	var sites geodata.GeoSiteList
	if err := proto.Unmarshal(siteData, &sites); err != nil {
		return nil, fmt.Errorf("decode routing geosite: %w", err)
	}
	if len(sites.Entry) != 2 {
		return nil, fmt.Errorf("routing geosite requires exactly CN and GEOLOCATION-!CN")
	}
	r := new(Rules)
	for _, site := range sites.Entry {
		if site == nil || len(site.Domain) == 0 {
			return nil, fmt.Errorf("empty routing geosite")
		}
		var target *geodata.DomainMatcher
		switch site.Code {
		case "CN":
			target = &r.domestic
		case "GEOLOCATION-!CN":
			target = &r.foreign
		default:
			return nil, fmt.Errorf("unexpected routing geosite code %q", site.Code)
		}
		if *target != nil {
			return nil, fmt.Errorf("duplicate routing geosite %q", site.Code)
		}
		rules := make([]*geodata.DomainRule, len(site.Domain))
		for i, domain := range site.Domain {
			if domain == nil || domain.Value == "" {
				return nil, fmt.Errorf("empty domain in routing geosite %s", site.Code)
			}
			rules[i] = &geodata.DomainRule{Value: &geodata.DomainRule_Custom{Custom: domain}}
		}
		// In the pinned Xray version, a factory with only Custom rules never uses
		// its shared geosite cache. This avoids global registries, file loaders,
		// environment variables and any dependency on the process working directory.
		matcher, err := new(geodata.MphDomainMatcherFactory).BuildMatcher(rules)
		if err != nil {
			return nil, fmt.Errorf("build routing geosite %s: %w", site.Code, err)
		}
		*target = matcher
	}
	var ips geodata.GeoIPList
	if err := proto.Unmarshal(ipData, &ips); err != nil {
		return nil, fmt.Errorf("decode routing geoip: %w", err)
	}
	if len(ips.Entry) != 1 || ips.Entry[0] == nil {
		return nil, fmt.Errorf("routing geoip requires exactly CN")
	}
	entry := ips.Entry[0]
	if entry.Code != "CN" || entry.ReverseMatch || len(entry.Cidr) == 0 {
		return nil, fmt.Errorf("invalid CN routing geoip")
	}
	r.cn = make([]netip.Prefix, 0, len(entry.Cidr))
	for _, cidr := range entry.Cidr {
		if cidr == nil || len(cidr.Ip) != 4 || cidr.Prefix > 32 {
			return nil, fmt.Errorf("invalid IPv4 routing CIDR")
		}
		addr, _ := netip.AddrFromSlice(cidr.Ip)
		prefix := netip.PrefixFrom(addr, int(cidr.Prefix))
		if prefix != prefix.Masked() {
			return nil, fmt.Errorf("unmasked IPv4 routing CIDR")
		}
		r.cn = append(r.cn, prefix)
	}
	sort.Slice(r.cn, func(i, j int) bool {
		if cmp := r.cn[i].Addr().Compare(r.cn[j].Addr()); cmp != 0 {
			return cmp < 0
		}
		return r.cn[i].Bits() < r.cn[j].Bits()
	})
	// A contained prefix must not hide its enclosing prefix during binary search.
	kept := r.cn[:0]
	for _, prefix := range r.cn {
		if len(kept) == 0 || !kept[len(kept)-1].Contains(prefix.Addr()) {
			kept = append(kept, prefix)
		}
	}
	r.cn = kept
	return r, nil
}

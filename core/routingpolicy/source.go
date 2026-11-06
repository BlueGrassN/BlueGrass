package routingpolicy

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

const (
	maxSourceBytes        = 32 << 20
	maxSourceEntries      = 250000
	maxSourceDomainLength = 4096
	maxSourceDomainBytes  = 8 << 20
)

// ImportSourceData validates full upstream geosite.dat and geoip.dat protobufs
// and selects CN / GEOLOCATION-!CN domains and noninverted CN native IPv4.
// Category names are case-insensitive; domain types and values are preserved.
// Each source is limited to 32 MiB, each selected group to 250,000 records,
// each domain value to 4 KiB, and combined selected domain text to 8 MiB.
// It does not mutate the input, use shared geodata caches, or perform I/O.
func ImportSourceData(siteData, ipData []byte) (*Rules, error) {
	for _, source := range []struct {
		name string
		data []byte
	}{{"geosite", siteData}, {"geoip", ipData}} {
		if len(source.data) == 0 {
			return nil, fmt.Errorf("empty routing %s source", source.name)
		}
		if len(source.data) > maxSourceBytes {
			return nil, fmt.Errorf("routing %s source exceeds byte limit", source.name)
		}
	}
	// Reduce one source at a time so unrelated decoded groups can be reclaimed
	// before decoding the next source and constructing the Xray matchers.
	site, err := selectSourceSites(siteData)
	if err != nil {
		return nil, err
	}
	ip, err := selectSourceIPs(ipData)
	if err != nil {
		return nil, err
	}
	// This also validates matcher types/regexes, sorts and removes contained or
	// duplicate IPv4 prefixes, and builds only custom (never file-backed) rules.
	return decodeRules(site, ip)
}

func selectSourceSites(data []byte) ([]byte, error) {
	var source geodata.GeoSiteList
	if err := (proto.UnmarshalOptions{DiscardUnknown: true, RecursionLimit: 32}).Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("decode routing geosite source: %w", err)
	}
	codes := [...]string{"CN", "GEOLOCATION-!CN"}
	selected := make([]*geodata.GeoSite, len(codes))
	for _, site := range source.Entry {
		if site == nil {
			continue
		}
		for i, code := range codes {
			if !strings.EqualFold(site.Code, code) {
				continue
			}
			if selected[i] != nil {
				return nil, fmt.Errorf("duplicate routing geosite source %s", code)
			}
			selected[i] = site
			site.Code = code
		}
	}
	totalBytes := 0
	for i, site := range selected {
		if site == nil || len(site.Domain) == 0 {
			return nil, fmt.Errorf("missing or empty routing geosite source %s", codes[i])
		}
		if len(site.Domain) > maxSourceEntries {
			return nil, fmt.Errorf("routing geosite source %s exceeds entry limit", site.Code)
		}
		for _, domain := range site.Domain {
			if domain == nil || domain.Value == "" {
				return nil, fmt.Errorf("empty domain in routing geosite source %s", site.Code)
			}
			if len(domain.Value) > maxSourceDomainLength {
				return nil, fmt.Errorf("routing geosite source %s exceeds domain length limit", site.Code)
			}
			totalBytes += len(domain.Value)
			if totalBytes > maxSourceDomainBytes {
				return nil, fmt.Errorf("routing geosite source exceeds domain text limit")
			}
			// Category selection is complete; attributes must not filter its members.
			domain.Attribute = nil
		}
		slices.SortFunc(site.Domain, func(a, b *geodata.Domain) int {
			if c := cmp.Compare(a.Type, b.Type); c != 0 {
				return c
			}
			return strings.Compare(a.Value, b.Value)
		})
		site.Domain = slices.CompactFunc(site.Domain, func(a, b *geodata.Domain) bool {
			return a.Type == b.Type && a.Value == b.Value
		})
	}
	return proto.Marshal(&geodata.GeoSiteList{Entry: selected})
}

func selectSourceIPs(data []byte) ([]byte, error) {
	var source geodata.GeoIPList
	if err := (proto.UnmarshalOptions{DiscardUnknown: true, RecursionLimit: 32}).Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("decode routing geoip source: %w", err)
	}
	var cn *geodata.GeoIP
	for _, entry := range source.Entry {
		if entry == nil || !strings.EqualFold(entry.Code, "CN") {
			continue
		}
		if cn != nil {
			return nil, fmt.Errorf("duplicate routing geoip source CN")
		}
		cn = entry
	}
	if cn == nil || cn.ReverseMatch || len(cn.Cidr) == 0 {
		return nil, fmt.Errorf("missing, inverted or empty routing geoip source CN")
	}
	if len(cn.Cidr) > maxSourceEntries {
		return nil, fmt.Errorf("routing geoip source CN exceeds entry limit")
	}
	ipv4 := cn.Cidr[:0]
	for _, cidr := range cn.Cidr {
		if cidr == nil {
			return nil, fmt.Errorf("nil CIDR in routing geoip source CN")
		}
		switch len(cidr.Ip) {
		case 4:
			if cidr.Prefix > 32 {
				return nil, fmt.Errorf("invalid IPv4 prefix in routing geoip source CN")
			}
			addr, _ := netip.AddrFromSlice(cidr.Ip)
			masked := netip.PrefixFrom(addr, int(cidr.Prefix)).Masked().Addr().As4()
			copy(cidr.Ip, masked[:])
			ipv4 = append(ipv4, cidr)
		case 16:
			if cidr.Prefix > 128 {
				return nil, fmt.Errorf("invalid IPv6 prefix in routing geoip source CN")
			}
			// Validate but exclude IPv6, including IPv4-mapped IPv6.
		default:
			return nil, fmt.Errorf("invalid IP length in routing geoip source CN")
		}
	}
	if len(ipv4) == 0 {
		return nil, fmt.Errorf("routing geoip source CN has no native IPv4")
	}
	clear(cn.Cidr[len(ipv4):])
	cn.Code, cn.Cidr = "CN", ipv4
	return proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{cn}})
}

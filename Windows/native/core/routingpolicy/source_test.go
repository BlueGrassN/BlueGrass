package routingpolicy

import (
	"bytes"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/encoding/protowire"
)

// Includes unrelated (even semantically invalid) categories and both IP families,
// as full upstream files do. Selected groups are deliberately not first or sorted.
func fullSourceFixture() (*geodata.GeoSiteList, *geodata.GeoIPList) {
	sites := &geodata.GeoSiteList{Entry: []*geodata.GeoSite{
		{Code: "OTHER", Domain: []*geodata.Domain{{Type: 99, Value: "["}}},
		{Code: "geolocation-!Cn", Domain: []*geodata.Domain{
			{Type: geodata.Domain_Domain, Value: "shared.test"},
			{Type: geodata.Domain_Domain, Value: "foreign.domestic.test"},
			{Type: geodata.Domain_Full, Value: "exact.foreign.test"},
			{Type: geodata.Domain_Substr, Value: "blockedkey"},
			{Type: geodata.Domain_Regex, Value: `^blocked[0-9]+\.test$`},
		}},
		{Code: "cN", Domain: []*geodata.Domain{
			{Type: geodata.Domain_Full, Value: "EXACT.test"},
			{Type: geodata.Domain_Domain, Value: "domestic.test", Attribute: []*geodata.Domain_Attribute{{Key: "outside"}}},
			{Type: geodata.Domain_Substr, Value: "keyWORD"},
			{Type: geodata.Domain_Regex, Value: `^rx[0-9]+\.test$`},
			{Type: geodata.Domain_Domain, Value: "shared.test"},
			{Type: geodata.Domain_Domain, Value: "xn--fiqs8s"},
			{Type: geodata.Domain_Regex, Value: `^CASE\.test$`},
			{Type: geodata.Domain_Domain, Value: "domestic.test", Attribute: []*geodata.Domain_Attribute{{Key: "another"}}},
			{Type: geodata.Domain_Full, Value: "same.test"},
			{Type: geodata.Domain_Domain, Value: "same.test"},
		}},
		{Code: "OTHER"},
	}}
	ips := &geodata.GeoIPList{Entry: []*geodata.GeoIP{
		{Code: "US", ReverseMatch: true, Cidr: []*geodata.CIDR{{Ip: []byte{1}, Prefix: 999}}},
		{Code: "cn", Cidr: []*geodata.CIDR{
			{Ip: []byte{223, 5, 5, 9}, Prefix: 24},
			{Ip: []byte{1, 0, 3, 9}, Prefix: 24},
			{Ip: netip.MustParseAddr("2001:db8::1234").AsSlice(), Prefix: 32},
			{Ip: []byte{1, 0, 2, 7}, Prefix: 23},
			{Ip: []byte{1, 0, 2, 0}, Prefix: 23},
			{Ip: netip.MustParseAddr("::ffff:8.8.8.8").AsSlice(), Prefix: 120},
			{Ip: []byte{1, 0, 1, 0}, Prefix: 24},
			{Ip: []byte{223, 5, 5, 0}, Prefix: 24},
		}},
		{Code: "US"},
	}}
	return sites, ips
}

func TestImportSourceDataPreservesMatchersAndPriority(t *testing.T) {
	s, i := fullSourceFixture()
	a, b := marshalFixture(t, s, i)
	originalA, originalB := bytes.Clone(a), bytes.Clone(b)
	r, err := ImportSourceData(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want Decision
	}{
		{"domestic.test", Direct}, {"sub.domestic.test", Direct}, {"notdomestic.test", Unknown},
		{"exact.test", Direct}, {"sub.exact.test", Unknown}, {"keyword-anywhere.test", Direct},
		{"rx42.test", Direct}, {"sub.rx42.test", Unknown}, {"case.test", Unknown},
		{"foreign.domestic.test", Proxy}, {"shared.test", Proxy}, {"sub.shared.test", Proxy},
		{"exact.foreign.test", Proxy}, {"sub.exact.foreign.test", Unknown},
		{"blockedkey-anywhere.test", Proxy}, {"blocked42.test", Proxy}, {"sub.blocked42.test", Unknown},
		{"example.中国", Direct}, {"EXACT.TEST.", Direct}, {"unrelated.test", Unknown},
		{"same.test", Direct}, {"sub.same.test", Direct},
	} {
		if got := r.Domain(tc.host); got != tc.want {
			t.Errorf("Domain(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
	if got := len(r.domestic.Match("domestic.test")); got != 1 {
		t.Errorf("attribute-only duplicates retained: %d matches", got)
	}
	if got := len(r.domestic.Match("same.test")); got != 2 {
		t.Errorf("different matcher types with the same value lost: %d matches", got)
	}
	if r.ShouldDirect("shared.test", netip.MustParseAddr("223.5.5.5")) {
		t.Error("CN IP overrode foreign domain priority")
	}
	if !r.ShouldDirect("domestic.test", netip.MustParseAddr("8.8.8.8")) {
		t.Error("CN domain no longer permits a non-CN public IPv4")
	}
	if !bytes.Equal(a, originalA) || !bytes.Equal(b, originalB) {
		t.Error("import mutated caller-owned source bytes")
	}

	// Sorting/deduplication must make matcher rule IDs independent of source order.
	for _, site := range s.Entry {
		slices.Reverse(site.Domain)
	}
	slices.Reverse(s.Entry)
	slices.Reverse(i.Entry)
	a, b = marshalFixture(t, s, i)
	reordered, err := ImportSourceData(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"same.test", "domestic.test", "keyword.test", "rx42.test"} {
		if !slices.Equal(r.domestic.Match(host), reordered.domestic.Match(host)) {
			t.Errorf("source order changed matcher IDs for %q", host)
		}
	}
}

func TestImportSourceDataCanonicalizesNativeIPv4(t *testing.T) {
	s, i := fullSourceFixture()
	a, b := marshalFixture(t, s, i)
	r, err := ImportSourceData(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("1.0.1.0/24"),
		netip.MustParsePrefix("1.0.2.0/23"),
		netip.MustParsePrefix("223.5.5.0/24"),
	}
	if !slices.Equal(r.cn, want) {
		t.Fatalf("CN prefixes = %v, want %v", r.cn, want)
	}
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"1.0.0.255", false}, {"1.0.1.0", true}, {"1.0.3.255", true}, {"1.0.4.0", false},
		{"223.5.5.0", true}, {"223.5.5.255", true}, {"223.5.6.0", false},
		{"8.8.8.8", false}, {"::ffff:223.5.5.5", false}, {"2001:db8::1", false},
	} {
		if got := r.IsCN(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("IsCN(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}

	i.Entry[1].Cidr = []*geodata.CIDR{{Ip: []byte{1, 2, 3, 4}, Prefix: 0}}
	a, b = marshalFixture(t, s, i)
	r, err = ImportSourceData(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsCN(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("valid IPv4 /0 was not masked")
	}
	for _, addr := range specialIPs() {
		if r.IsCN(addr) || r.ShouldDirect("domestic.test", addr) {
			t.Errorf("import bypassed public IPv4 gate for %s", addr)
		}
	}
}

func TestImportSourceDataRejectsInvalidSelectedRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*geodata.GeoSiteList, *geodata.GeoIPList)
	}{
		{"missing_cn_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[2].Code = "OTHER" }},
		{"missing_foreign_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[1].Code = "OTHER" }},
		{"duplicate_cn_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[0].Code = "CN" }},
		{"duplicate_foreign_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[0].Code = "GEOLOCATION-!CN" }},
		{"empty_cn", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[2].Domain = nil }},
		{"empty_foreign", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[1].Domain = nil }},
		{"nil_domain", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[2].Domain[0] = nil }},
		{"empty_domain", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[2].Domain[0].Value = "" }},
		{"unknown_matcher", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[2].Domain[0].Type = 99 }},
		{"negative_matcher", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[1].Domain[0].Type = -1 }},
		{"bad_cn_regex", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[2].Domain[3].Value = "[" }},
		{"bad_foreign_regex", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[1].Domain[4].Value = "[" }},
		{"missing_cn_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Code = "US" }},
		{"duplicate_cn_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[2].Code = "CN" }},
		{"duplicate_inverted_cn_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Code = "CN" }},
		{"inverted_cn_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].ReverseMatch = true }},
		{"empty_cn_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Cidr = nil }},
		{"nil_cidr", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Cidr[0] = nil }},
		{"bad_address_length", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Cidr[0].Ip = []byte{1, 2, 3} }},
		{"bad_v4_prefix", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Cidr[0].Prefix = 33 }},
		{"bad_v6_prefix", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Cidr[2].Prefix = 129 }},
		{"bad_mapped_v6_prefix", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[1].Cidr[5].Prefix = 129 }},
		{"no_native_v4", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) {
			i.Entry[1].Cidr = []*geodata.CIDR{i.Entry[1].Cidr[2], i.Entry[1].Cidr[5]}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, i := fullSourceFixture()
			tc.change(s, i)
			a, b := marshalFixture(t, s, i)
			if r, err := ImportSourceData(a, b); err == nil || r != nil {
				t.Fatalf("accepted invalid source: rules=%v err=%v", r, err)
			}
		})
	}
}

func TestImportSourceDataRejectsCorruption(t *testing.T) {
	s, i := fullSourceFixture()
	a, b := marshalFixture(t, s, i)
	for _, source := range []string{"geosite", "geoip"} {
		valid := a
		if source == "geoip" {
			valid = b
		}
		for _, tc := range []struct {
			name string
			data []byte
		}{
			{"nil", nil}, {"empty", []byte{}}, {"bad_wire", []byte{0xff}},
			{"truncated", valid[:len(valid)-1]}, {"trailing_garbage", append(bytes.Clone(valid), 0)},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				x, y := a, b
				if source == "geosite" {
					x = tc.data
				} else {
					y = tc.data
				}
				if r, err := ImportSourceData(x, y); err == nil || r != nil {
					t.Fatalf("accepted corrupt %s: rules=%v err=%v", source, r, err)
				}
			})
		}
	}
}

func TestImportSourceDataLimits(t *testing.T) {
	t.Run("source_bytes", func(t *testing.T) {
		s, i := fullSourceFixture()
		a, b := marshalFixture(t, s, i)
		// A valid unknown field pads each source to the exact size boundary.
		pad := func(data []byte, size int) []byte {
			data = protowire.AppendTag(data, 100, protowire.BytesType)
			n := size - len(data) - 4 // This padding length uses a four-byte varint.
			return protowire.AppendBytes(data, make([]byte, n))
		}
		for _, source := range []string{"geosite", "geoip"} {
			x, y := a, b
			if source == "geosite" {
				x = pad(bytes.Clone(a), 32<<20)
			} else {
				y = pad(bytes.Clone(b), 32<<20)
			}
			if _, err := ImportSourceData(x, y); err != nil {
				t.Fatalf("%s exact byte limit rejected: %v", source, err)
			}
			if source == "geosite" {
				x = pad(bytes.Clone(a), (32<<20)+1)
			} else {
				y = pad(bytes.Clone(b), (32<<20)+1)
			}
			if r, err := ImportSourceData(x, y); err == nil || r != nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("%s byte limit not enforced: rules=%v err=%v", source, r, err)
			}
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*geodata.GeoSiteList, *geodata.GeoIPList)
	}{
		{"cn_entries", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) {
			s.Entry[2].Domain = slices.Repeat(s.Entry[2].Domain[:1], 250001)
		}},
		{"foreign_entries", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) {
			s.Entry[1].Domain = slices.Repeat(s.Entry[1].Domain[:1], 250001)
		}},
		{"cidr_entries", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) {
			i.Entry[1].Cidr = slices.Repeat(i.Entry[1].Cidr[2:3], 250001)
		}},
		{"cn_value_length", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) {
			s.Entry[2].Domain[0].Value = strings.Repeat("x", 4097)
		}},
		{"foreign_value_length", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) {
			s.Entry[1].Domain[0].Value = strings.Repeat("x", 4097)
		}},
		{"total_domain_text", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) {
			d := &geodata.Domain{Type: geodata.Domain_Substr, Value: strings.Repeat("x", 4096)}
			s.Entry[1].Domain = slices.Repeat([]*geodata.Domain{d}, 1025)
			s.Entry[2].Domain = slices.Repeat([]*geodata.Domain{d}, 1025)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, i := fullSourceFixture()
			tc.change(s, i)
			a, b := marshalFixture(t, s, i)
			if r, err := ImportSourceData(a, b); err == nil || r != nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("selected record limit not enforced: rules=%v err=%v", r, err)
			}
		})
	}
}

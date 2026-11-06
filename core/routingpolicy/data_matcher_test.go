package routingpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func fixture() (*geodata.GeoSiteList, *geodata.GeoIPList) {
	return &geodata.GeoSiteList{Entry: []*geodata.GeoSite{
		{Code: "CN", Domain: []*geodata.Domain{
			{Type: geodata.Domain_Domain, Value: "domestic.test"},
			{Type: geodata.Domain_Domain, Value: "shared.test"},
			{Type: geodata.Domain_Domain, Value: "xn--fiqs8s"},
			{Type: geodata.Domain_Full, Value: "exact.test"},
			{Type: geodata.Domain_Substr, Value: "keyword"},
			{Type: geodata.Domain_Regex, Value: `^rx[0-9]+\.test$`},
		}},
		{Code: "GEOLOCATION-!CN", Domain: []*geodata.Domain{
			{Type: geodata.Domain_Domain, Value: "foreign.domestic.test"},
			{Type: geodata.Domain_Domain, Value: "shared.test"},
		}},
	}}, &geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "CN", Cidr: []*geodata.CIDR{
		{Ip: []byte{223, 5, 5, 0}, Prefix: 24},
		{Ip: []byte{1, 0, 2, 0}, Prefix: 24},
		{Ip: []byte{1, 0, 2, 0}, Prefix: 23},
		{Ip: []byte{1, 0, 1, 0}, Prefix: 24},
	}}}}
}

func marshalFixture(t *testing.T, site *geodata.GeoSiteList, ip *geodata.GeoIPList) ([]byte, []byte) {
	t.Helper()
	siteData, err := proto.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	ipData, err := proto.Marshal(ip)
	if err != nil {
		t.Fatal(err)
	}
	return siteData, ipData
}

func TestCustomXrayMatcherTypesAndForeignPriority(t *testing.T) {
	site, ip := fixture()
	a, b := marshalFixture(t, site, ip)
	r, err := decodeRules(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want Decision
	}{
		{"domestic.test", Direct}, {"sub.domestic.test", Direct}, {"notdomestic.test", Unknown},
		{"domestic.test.evil.invalid", Unknown}, {"foreign.domestic.test", Proxy},
		{"sub.foreign.domestic.test", Proxy}, {"shared.test", Proxy},
		{"exact.test", Direct}, {"sub.exact.test", Unknown}, {"keyword-anywhere.test", Direct},
		{"rx42.test", Direct}, {"sub.rx42.test", Unknown}, {"rx.test", Unknown},
		{"example.中国", Direct}, {"example.xn--fiqs8s", Direct},
		{strings.Repeat("a", 64) + ".domestic.test", Unknown}, {"-bad.domestic.test", Unknown},
		{"bad-.domestic.test", Unknown}, {"bad_label.domestic.test", Unknown},
	} {
		if got := r.Domain(tc.host); got != tc.want {
			t.Errorf("Domain(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
	if r.ShouldDirect("shared.test", netip.MustParseAddr("223.5.5.5")) {
		t.Error("CN IP overrode foreign domain")
	}
}

func TestPrefixBoundariesOverlapsAndGaps(t *testing.T) {
	site, ip := fixture()
	a, b := marshalFixture(t, site, ip)
	r, err := decodeRules(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"1.0.0.255", false}, {"1.0.1.0", true}, {"1.0.1.255", true}, {"1.0.2.0", true},
		{"1.0.3.255", true}, {"1.0.4.0", false}, {"223.5.4.255", false},
		{"223.5.5.0", true}, {"223.5.5.255", true}, {"223.5.6.0", false},
	} {
		if got := r.IsCN(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("IsCN(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestPublicIPv4GateEvenWithCatchAllGeoIP(t *testing.T) {
	site, ip := fixture()
	ip.Entry[0].Cidr = []*geodata.CIDR{{Ip: []byte{0, 0, 0, 0}, Prefix: 0}}
	a, b := marshalFixture(t, site, ip)
	r, err := decodeRules(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsCN(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("catch-all fixture not active")
	}
	for _, addr := range specialIPs() {
		if r.IsCN(addr) || r.ShouldDirect("domestic.test", addr) || r.ShouldDirect("unknown.test", addr) {
			t.Errorf("public gate bypassed for %s", addr)
		}
	}
}

func TestRejectMalformedRuleData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*geodata.GeoSiteList, *geodata.GeoIPList)
	}{
		{"missing_foreign", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry = s.Entry[:1] }},
		{"duplicate_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[1].Code = "CN" }},
		{"unexpected_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[0].Code = "OTHER" }},
		{"empty_site", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[0].Domain = nil }},
		{"empty_domain", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[0].Domain[0].Value = "" }},
		{"unknown_matcher", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) { s.Entry[0].Domain[0].Type = 99 }},
		{"invalid_regex", func(s *geodata.GeoSiteList, _ *geodata.GeoIPList) {
			s.Entry[0].Domain[0] = &geodata.Domain{Type: geodata.Domain_Regex, Value: "["}
		}},
		{"missing_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry = nil }},
		{"duplicate_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry = append(i.Entry, i.Entry[0]) }},
		{"wrong_ip_country", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Code = "US" }},
		{"reverse_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].ReverseMatch = true }},
		{"empty_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Cidr = nil }},
		{"invalid_ip", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Cidr[0].Ip = []byte{1, 2, 3} }},
		{"ipv6", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Cidr[0].Ip = make([]byte, 16) }},
		{"invalid_prefix", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Cidr[0].Prefix = 33 }},
		{"unmasked_prefix", func(_ *geodata.GeoSiteList, i *geodata.GeoIPList) { i.Entry[0].Cidr[0].Ip = []byte{223, 5, 5, 1} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, i := fixture()
			tc.change(s, i)
			a, b := marshalFixture(t, s, i)
			if r, err := decodeRules(a, b); err == nil || r != nil {
				t.Fatalf("accepted invalid data: rules=%v err=%v", r, err)
			}
		})
	}
	s, i := fixture()
	a, b := marshalFixture(t, s, i)
	for _, bad := range [][]byte{nil, {0xff}, a[:len(a)-1]} {
		if r, err := decodeRules(bad, b); err == nil || r != nil {
			t.Error("accepted malformed geosite")
		}
	}
	for _, bad := range [][]byte{nil, {0xff}, b[:len(b)-1]} {
		if r, err := decodeRules(a, bad); err == nil || r != nil {
			t.Error("accepted malformed geoip")
		}
	}
}

func TestBundledChecksumRejectsCorruption(t *testing.T) {
	site, err := bundled.ReadFile("assets/geosite-cn-foreign.dat")
	if err != nil {
		t.Fatal(err)
	}
	ip, err := bundled.ReadFile("assets/geoip-cn-v4.dat")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		site, ip []byte
	}{
		{"truncated_site", site[:len(site)-1], ip}, {"empty_ip", site, nil},
		{"replaced_site", append([]byte{site[0] ^ 1}, site[1:]...), ip},
		{"replaced_ip", site, append([]byte{ip[0] ^ 1}, ip[1:]...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r, err := loadBundled(tc.site, tc.ip); err == nil || r != nil {
				t.Fatal("accepted corrupted bundle")
			}
		})
	}
}

func TestBundledMetadataMatchesShippedData(t *testing.T) {
	data, err := bundled.ReadFile("assets/metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Version string `json:"version"`
		Assets  map[string]struct {
			SHA256  string         `json:"sha256"`
			Bytes   int            `json:"bytes"`
			Entries map[string]int `json:"entries"`
		} `json:"assets"`
		Licenses []struct{ Name, SHA256 string } `json:"licenses"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Version != Version {
		t.Fatal("metadata version differs from compiled version")
	}
	for _, name := range []string{"geosite-cn-foreign.dat", "geoip-cn-v4.dat"} {
		data, err := bundled.ReadFile("assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		entry := meta.Assets[name]
		if entry.SHA256 != hex.EncodeToString(sum[:]) || entry.Bytes != len(data) {
			t.Errorf("incorrect metadata for %s", name)
		}
		if entry.Entries["CN"] < 1000 {
			t.Errorf("%s is not a substantial CN dataset", name)
		}
	}
	if meta.Assets["geosite-cn-foreign.dat"].Entries["GEOLOCATION-!CN"] < 1000 {
		t.Fatal("foreign domain set is incomplete")
	}
	requiredLicenses := map[string]bool{
		"LICENSE-Loyalsoldier-v2ray-rules-dat-GPL-3.0.txt": false,
		"LICENSE-geosite-MIT.txt":                          false,
		"LICENSE-Loyalsoldier-domain-list-custom-MIT.txt":  false,
		"LICENSE-dnsmasq-china-list-WTFPL-2.0.txt":         false,
		"LICENSE-GFWList-LGPL-2.1.txt":                     false,
		"LICENSE-gfwlist2dnsmasq-GPL-3.0.txt":              false,
		"LICENSE-Loyalsoldier-geoip-CC-BY-SA-4.0.txt":      false,
		"LICENSE-Loyalsoldier-geoip-GPL-3.0.txt":           false,
		"LICENSE-china-operator-ip-MIT.txt":                false,
		"LICENSE-Xray-MPL-2.0.txt":                         false,
	}
	for _, license := range meta.Licenses {
		requiredLicenses[license.Name] = true
		data, err := bundled.ReadFile("assets/licenses/" + license.Name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != license.SHA256 {
			t.Errorf("license mismatch: %s", license.Name)
		}
	}
	for name, found := range requiredLicenses {
		if !found {
			t.Errorf("missing required license metadata: %s", name)
		}
	}
}

func TestUninitializedRulesFailClosed(t *testing.T) {
	for _, r := range []*Rules{nil, {}} {
		if r.Domain("baidu.com") != Unknown || r.IsCN(netip.MustParseAddr("223.5.5.5")) || r.ShouldDirect("baidu.com", netip.MustParseAddr("223.5.5.5")) {
			t.Fatal("uninitialized rules permitted direct routing")
		}
	}
}

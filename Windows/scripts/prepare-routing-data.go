//go:build ignore

// Run from Windows/native/core:
// go run ../../scripts/prepare-routing-data.go [-check] [-offline]
// This maintenance tool is never imported by the application.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

const xrayVersion = "v1.260327.1-0.20261017152356-92ccc939aaf4"
const dataVersion = "loyalsoldier-202610232206_v1"

type source struct {
	Name       string `json:"name"`
	Repository string `json:"repository,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Commit     string `json:"commit,omitempty"`
	URL        string `json:"url"`
	APIURL     string `json:"api_url,omitempty"`
	SHA256     string `json:"sha256"`
	License    string `json:"license"`
	ContentAPI bool   `json:"-"`
}

var dataSources = []source{
	{Name: "geosite.dat", Repository: "Loyalsoldier/v2ray-rules-dat", Tag: "202610232206", Commit: "b8ee45341f4995e9bcfd5babe667412841a43ffa", URL: "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/202610232206/geosite.dat", APIURL: "https://api.github.com/repos/Loyalsoldier/v2ray-rules-dat/releases/assets/670547158", SHA256: "9cb5ab39ce601ad17d4077909b4d4e943c212af7baa658a612adc64e1c201ca4", License: "GPL-3.0; upstream component notices retained"},
	{Name: "geoip.dat", Repository: "Loyalsoldier/v2ray-rules-dat", Tag: "202610232206", Commit: "b8ee45341f4995e9bcfd5babe667412841a43ffa", URL: "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/202610232206/geoip.dat", APIURL: "https://api.github.com/repos/Loyalsoldier/v2ray-rules-dat/releases/assets/670547231", SHA256: "a6271d3c96361d96156e6d3f7cf1550ef21b00d2f951deb772f464db91e89ac5", License: "GPL-3.0 (rules-dat); CC-BY-SA-4.0 and GPL-3.0 (Loyalsoldier/geoip declarations); CN IPv4 input MIT (gaoyifan/china-operator-ip)"},
}

var licenseSources = []source{
	{Name: "LICENSE-Loyalsoldier-v2ray-rules-dat-GPL-3.0.txt", URL: "https://api.github.com/repos/Loyalsoldier/v2ray-rules-dat/contents/LICENSE?ref=b8ee45341f4995e9bcfd5babe667412841a43ffa", SHA256: "3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986", License: "GPL-3.0", ContentAPI: true},
	{Name: "LICENSE-geosite-MIT.txt", URL: "https://api.github.com/repos/v2fly/domain-list-community/contents/LICENSE?ref=9cad4889b6f1c51bd84cb8a0d95dbc1a992251a4", SHA256: "b9d84a22870d3f21c91a4c6e410c9cc51d00902f5233ad0c84011479244bf7d2", License: "MIT", ContentAPI: true},
	{Name: "LICENSE-Loyalsoldier-domain-list-custom-MIT.txt", URL: "https://api.github.com/repos/Loyalsoldier/domain-list-custom/contents/LICENSE?ref=efacb51b8950ae673ebb6dcb9e7ecdd1decb1b6d", SHA256: "35f18e0331a1ecd1835400c50e3b367c2ce09f6c13d91c4a0f3cb11f71d3bbc3", License: "MIT", ContentAPI: true},
	{Name: "LICENSE-dnsmasq-china-list-WTFPL-2.0.txt", URL: "https://api.github.com/repos/felixonmars/dnsmasq-china-list/contents/LICENSE?ref=eb9dc5e02c20f9dbcb81e2f868c6cbfc5fc1727f", SHA256: "f9e00539c44942d94e3c6d20f0c90e63e810ea181b10f16a252c93e4ea5cefd7", License: "WTFPL", ContentAPI: true},
	{Name: "LICENSE-GFWList-LGPL-2.1.txt", URL: "https://api.github.com/repos/gfwlist/gfwlist/contents/COPYING.txt?ref=8b765c742ae7b57ef3dde1e41b49b294c182c922", SHA256: "dc626520dcd53a22f727af3ee42c770e56c97a64fe3adb063799d8ab032fe551", License: "LGPL-2.1", ContentAPI: true},
	{Name: "LICENSE-gfwlist2dnsmasq-GPL-3.0.txt", URL: "https://api.github.com/repos/cokebar/gfwlist2dnsmasq/contents/LICENSE?ref=202e52fb5ea2260abc9f162b3a78e58ec9187005", SHA256: "589ed823e9a84c56feb95ac58e7cf384626b9cbf4fda2a907bc36e103de1bad2", License: "GPL-3.0", ContentAPI: true},
	{Name: "LICENSE-Loyalsoldier-geoip-CC-BY-SA-4.0.txt", URL: "https://api.github.com/repos/Loyalsoldier/geoip/contents/LICENSE?ref=084eb113e458b82374919dabefbc49cd2c2dd079", SHA256: "5e436ff8ffbb77d8607220e9bce20c8915d860010feeb6c1ebef5a85688e9b39", License: "CC-BY-SA-4.0", ContentAPI: true},
	{Name: "LICENSE-Loyalsoldier-geoip-GPL-3.0.txt", URL: "https://api.github.com/repos/Loyalsoldier/geoip/contents/LICENSE-GPL?ref=084eb113e458b82374919dabefbc49cd2c2dd079", SHA256: "3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986", License: "GPL-3.0", ContentAPI: true},
	{Name: "LICENSE-china-operator-ip-MIT.txt", URL: "https://api.github.com/repos/gaoyifan/china-operator-ip/contents/LICENSE?ref=b2b8edc0ddd9cf2bc638ec77587301bbf84b8db9", SHA256: "8f4d03a581484143945a9fac471c3b669d5aada68361fd1f25886c3ddbdd786a", License: "MIT", ContentAPI: true},
	{Name: "LICENSE-Xray-MPL-2.0.txt", URL: "https://api.github.com/repos/XTLS/Xray-core/contents/LICENSE?ref=92ccc939aaf4f8965f369cf0b142e005aeaa11d5", SHA256: "1f256ecad192880510e84ad60474eab7589218784b9a50bc7ceee34c2b91f1d5", License: "MPL-2.0", ContentAPI: true},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	output := flag.String("out", "routingpolicy", "package directory")
	cache := flag.String("cache", "../../.artifacts/routing-data-sources", "verified source download cache (not shipped)")
	check := flag.Bool("check", false, "compare generated bytes without writing package files")
	offline := flag.Bool("offline", false, "require cached sources; never use the network")
	flag.Parse()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("Go build information unavailable")
	}
	found := false
	for _, dep := range info.Deps {
		if dep.Path == "github.com/xtls/xray-core" {
			if dep.Version != xrayVersion || dep.Replace != nil {
				return fmt.Errorf("require unmodified Xray %s", xrayVersion)
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("pinned Xray dependency unavailable")
	}
	inputs := make([][]byte, len(dataSources))
	for i, spec := range dataSources {
		data, err := fetch(spec, *cache, *offline)
		if err != nil {
			return err
		}
		inputs[i] = data
	}
	sites, counts, err := filterSites(inputs[0])
	if err != nil {
		return err
	}
	ips, ipCounts, err := filterIPs(inputs[1])
	if err != nil {
		return err
	}
	files := map[string][]byte{"assets/geosite-cn-foreign.dat": sites, "assets/geoip-cn-v4.dat": ips}
	for _, spec := range licenseSources {
		data, err := fetch(spec, *cache, *offline)
		if err != nil {
			return err
		}
		files["assets/licenses/"+spec.Name] = data
	}
	type asset struct {
		SHA256  string         `json:"sha256"`
		Bytes   int            `json:"bytes"`
		Entries map[string]int `json:"entries"`
	}
	metadata := struct {
		Format     int              `json:"format"`
		Version    string           `json:"version"`
		XrayModule string           `json:"xray_module"`
		Sources    []source         `json:"sources"`
		Licenses   []source         `json:"licenses"`
		Assets     map[string]asset `json:"assets"`
		Transform  string           `json:"transform"`
	}{1, dataVersion, xrayVersion, dataSources, licenseSources, map[string]asset{
		"geosite-cn-foreign.dat": {digest(sites), len(sites), counts},
		"geoip-cn-v4.dat":        {digest(ips), len(ips), ipCounts},
	}, "Select CN and GEOLOCATION-!CN domains; discard attributes after selection; deduplicate identical type/value pairs and sort by type/value. Preserve all four Xray matcher types. Select CN IPv4 CIDRs only; mask, sort, deduplicate, remove contained prefixes. Marshal deterministic protobuf. No runtime downloads."}
	meta, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	files["assets/metadata.json"] = append(meta, '\n')
	generated, err := format.Source([]byte(fmt.Sprintf(`// Code generated by Windows/scripts/prepare-routing-data.go; DO NOT EDIT.
package routingpolicy

// Version identifies the pinned data releases and the compact format revision.
const Version = %q

const geositeSHA256 = %q
const geoipSHA256 = %q
`, dataVersion, digest(sites), digest(ips))))
	if err != nil {
		return err
	}
	files["data_generated.go"] = generated
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(*output, filepath.FromSlash(name))
		if *check {
			current, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, files[name]) {
				return fmt.Errorf("generated content differs: %s", path)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(path, files[name], 0644); err != nil {
				return err
			}
		}
		fmt.Printf("%s %d bytes sha256:%s\n", name, len(files[name]), digest(files[name]))
	}
	fmt.Printf("domains=%v IPv4=%v\n", counts, ipCounts)
	return nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func fetch(spec source, cache string, offline bool) ([]byte, error) {
	path := filepath.Join(cache, spec.Name)
	if data, err := os.ReadFile(path); err == nil {
		if digest(data) != spec.SHA256 {
			return nil, fmt.Errorf("cached source hash mismatch: %s", spec.Name)
		}
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if offline {
		return nil, fmt.Errorf("offline source missing: %s", spec.Name)
	}
	url, accept := spec.URL, "application/vnd.github+json"
	if spec.APIURL != "" {
		url, accept = spec.APIURL, "application/octet-stream"
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "BlueGrass-routing-data")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", spec.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", spec.Name, resp.StatusCode)
	}
	const limit = 16 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("source too large: %s", spec.Name)
	}
	if spec.ContentAPI {
		var content struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := json.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		if content.Encoding != "base64" {
			return nil, fmt.Errorf("unexpected content encoding: %s", spec.Name)
		}
		data, err = base64.StdEncoding.DecodeString(content.Content)
		if err != nil {
			return nil, err
		}
	}
	if digest(data) != spec.SHA256 {
		return nil, fmt.Errorf("download hash mismatch: %s", spec.Name)
	}
	if err := os.MkdirAll(cache, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, err
	}
	return data, nil
}

func filterSites(raw []byte) ([]byte, map[string]int, error) {
	var input geodata.GeoSiteList
	if err := proto.Unmarshal(raw, &input); err != nil {
		return nil, nil, err
	}
	output := new(geodata.GeoSiteList)
	counts := make(map[string]int)
	for _, code := range []string{"CN", "GEOLOCATION-!CN"} {
		var selected *geodata.GeoSite
		for _, entry := range input.Entry {
			if strings.EqualFold(entry.Code, code) {
				if selected != nil {
					return nil, nil, fmt.Errorf("duplicate geosite %s", code)
				}
				selected = entry
			}
		}
		if selected == nil || len(selected.Domain) == 0 {
			return nil, nil, fmt.Errorf("empty geosite %s", code)
		}
		domains := make([]*geodata.Domain, 0, len(selected.Domain))
		seen := make(map[string]bool)
		for _, d := range selected.Domain {
			if d == nil || d.Value == "" {
				return nil, nil, fmt.Errorf("empty domain in %s", code)
			}
			key := fmt.Sprintf("%d:%s", d.Type, d.Value)
			if seen[key] {
				continue
			}
			seen[key] = true
			domains = append(domains, &geodata.Domain{Type: d.Type, Value: d.Value})
		}
		sort.Slice(domains, func(i, j int) bool {
			if domains[i].Type != domains[j].Type {
				return domains[i].Type < domains[j].Type
			}
			return domains[i].Value < domains[j].Value
		})
		rules := make([]*geodata.DomainRule, len(domains))
		for i, domain := range domains {
			rules[i] = &geodata.DomainRule{Value: &geodata.DomainRule_Custom{Custom: domain}}
		}
		if _, err := new(geodata.MphDomainMatcherFactory).BuildMatcher(rules); err != nil {
			return nil, nil, fmt.Errorf("geosite %s: %w", code, err)
		}
		output.Entry = append(output.Entry, &geodata.GeoSite{Code: code, Domain: domains})
		counts[code] = len(domains)
		for _, d := range domains {
			counts[code+"/"+d.Type.String()]++
		}
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(output)
	return data, counts, err
}

func filterIPs(raw []byte) ([]byte, map[string]int, error) {
	var input geodata.GeoIPList
	if err := proto.Unmarshal(raw, &input); err != nil {
		return nil, nil, err
	}
	var selected *geodata.GeoIP
	for _, entry := range input.Entry {
		if strings.EqualFold(entry.Code, "CN") {
			if selected != nil {
				return nil, nil, fmt.Errorf("duplicate CN geoip")
			}
			selected = entry
		}
	}
	if selected == nil || selected.ReverseMatch {
		return nil, nil, fmt.Errorf("missing or inverted CN geoip")
	}
	prefixes := make([]netip.Prefix, 0, len(selected.Cidr))
	for _, cidr := range selected.Cidr {
		if cidr == nil {
			return nil, nil, fmt.Errorf("nil CIDR")
		}
		addr, ok := netip.AddrFromSlice(cidr.Ip)
		if !ok || cidr.Prefix > uint32(addr.BitLen()) {
			return nil, nil, fmt.Errorf("invalid CIDR")
		}
		if addr.Is4() {
			prefixes = append(prefixes, netip.PrefixFrom(addr, int(cidr.Prefix)).Masked())
		}
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if cmp := prefixes[i].Addr().Compare(prefixes[j].Addr()); cmp != 0 {
			return cmp < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})
	kept := prefixes[:0]
	for _, prefix := range prefixes {
		if len(kept) == 0 || !kept[len(kept)-1].Contains(prefix.Addr()) {
			kept = append(kept, prefix)
		}
	}
	if len(kept) == 0 {
		return nil, nil, fmt.Errorf("empty CN IPv4 set")
	}
	entry := &geodata.GeoIP{Code: "CN"}
	for _, prefix := range kept {
		addr := prefix.Addr().As4()
		entry.Cidr = append(entry.Cidr, &geodata.CIDR{Ip: addr[:], Prefix: uint32(prefix.Bits())})
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{entry}})
	return data, map[string]int{"CN": len(kept), "source_CIDRs_including_IPv6": len(selected.Cidr)}, err
}

# Shared Android and Windows routing policy

This package is the Android and Windows native cores' shared immutable geodata
sidecar. It uses the
existing `github.com/xtls/xray-core` module, pinned to
`v1.260327.1-0.20261017152356-92ccc939aaf4`. No second proxy core is added.

## API and routing contract

```go
type Mode string
const Global Mode = "global"
const BypassCN Mode = "bypass_cn"
func Normalize(value string) (Mode, error)

type Decision uint8
const (Unknown Decision = iota; Direct; Proxy)

const DomesticDNS = "223.5.5.5"
const Version = "loyalsoldier-202610232206_v1"

func Load() (*Rules, error)
func (*Rules) Domain(host string) Decision
func (*Rules) IsCN(ip netip.Addr) bool
func (*Rules) ShouldDirect(domain string, ip netip.Addr) bool
```

`Normalize("")` returns `Global` for legacy configurations. Unknown strings,
including different case or surrounding whitespace, return an error.

`Load` verifies compiled SHA-256 expectations before decoding embedded protobufs
and builds one immutable singleton. Concurrent callers share its pointer and any
initialization error. There is no runtime downloading, reloading, filesystem
lookup, environment override, working-directory change or Xray registry mutation.

`Domain` normalizes case, one trailing DNS root dot and IDNA hostnames, then checks
`geolocation-!cn` before `cn`. It rejects malformed names, IP literals, URLs and
host:port strings as `Unknown`. The pinned Xray
`common/geodata.MphDomainMatcherFactory.BuildMatcher` receives only
`DomainRule_Custom` entries decoded from the bundle. That API's custom-only path
does not access its shared file cache. Suffix, exact, substring and regex rules
retain their upstream matching semantics; suffix rules match only whole DNS
labels. Regex and substring rules intentionally retain their own semantics.

`ShouldDirect` first requires `gate.PublicIPv4(ip.String())`. A `Proxy` domain
always proxies, a `Direct` domain permits **any eligible public IPv4**, and an
`Unknown` domain falls back to `IsCN`. IPv6 (including mapped IPv4), private,
loopback, link-local, CGNAT, FakeIP/benchmark, documentation, multicast and reserved
addresses denied by the existing gate never pass. This classification grants no
WFP or OS permission. Callers must resolve once, classify that literal address,
and dial the same address. DNS selection, connection modes and OS enforcement are
integrated separately in the parent-owned `resolve.go` and engine packages.

CN prefixes are sorted and contained prefixes removed before binary search.
The pinned Loyalsoldier CN source includes `119.29.29.29`. This is a data-source
classification, with no resolver-specific exception. Foreign-domain priority
still applies even when that domain resolves to a CN IPv4 address.

## Pinned data and provenance

| Source | Release | SHA-256 of original download |
| --- | --- | --- |
| [Loyalsoldier geosite.dat](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202610232206) | `202610232206` | `9cb5ab39ce601ad17d4077909b4d4e943c212af7baa658a612adc64e1c201ca4` |
| [Loyalsoldier geoip.dat](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202610232206) | `202610232206` | `a6271d3c96361d96156e6d3f7cf1550ef21b00d2f951deb772f464db91e89ac5` |

This is the default data provider configured by
[v2rayN's GeoUrl](https://github.com/2dust/v2rayN/blob/master/v2rayN/ServiceLib/Global.cs).
The generator selects all 110,243 `CN` and 27,463 `GEOLOCATION-!CN` rules from the
pinned geosite release, removes attributes after category selection, and sorts
and deduplicates identical `(type, value)` pairs. It retains 6,522 CN IPv4 prefixes
from 10,011 CN IPv4/IPv6 source records. It does not ship either full source blob.
The data-source switch preserves this package's existing routing contract; it
does not import v2rayN's other routing templates. `Load` remains an offline
bundle loader. Android separately uses `routingupdate.Store` and
`ImportSourceData` for verified downloadable snapshots; Windows callers keep
their existing bundled loading behavior.

| Embedded file | Bytes | SHA-256 |
| --- | ---: | --- |
| `assets/geosite-cn-foreign.dat` | 2,496,765 | `6e0213910bbee514dd2934c10017064ac44e6d42110ccd80ac0e121f586a2c12` |
| `assets/geoip-cn-v4.dat` | 65,228 | `9f2c27d565691f334ede744e267ebbe5cb9ad6a81f5d779552017d189b8e10ef` |

`assets/metadata.json` records source URLs, immutable release asset API URLs,
resolved commits, source/output hashes, sizes, rule-type counts, transformations
and license hashes. `data_generated.go` pins the runtime expectations.

## Reproduce and validate

From `E:/workspace/APP/core`, in PowerShell:

```powershell
$env:GOCACHE = 'E:/workspace/APP/Windows/.artifacts/go-cache-routing-data'
$go = 'D:/Program Files/Go/bin/go.exe'
& $go run ../../scripts/prepare-routing-data.go -check
& $go run ../../scripts/prepare-routing-data.go -check -offline
& $go test ./routingpolicy -race -count=1
& $go vet ./routingpolicy
```

Omit `-check` to regenerate. `-out` selects the package directory and `-cache`
selects the source cache (default `Windows/.artifacts/routing-data-sources`).
Only this explicitly invoked maintenance tool downloads public, pinned sources.
It verifies every cached/downloaded source and license, refuses hash mismatches,
requires the exact unreplaced Xray module, and deterministically marshals and
compares bytes. `-offline` forbids network use. Updating data requires a reviewed
change to the generator's release URLs/hashes and regeneration; there is no
"latest" URL in the generator or runtime.

Tests cover genuine bundled rules, suffix spoofing, normalization, all four Xray
matcher types, foreign-over-CN priority, domain-over-IP priority, public IPv4
eligibility even with a synthetic `/0`, prefix overlap/boundaries, concurrent
singleton access, checksum failures, malformed protobufs and rule shapes, and
metadata/license consistency. These are local package tests, not live proxy,
DNS, WFP, release-build or deployment acceptance.

## Attribution and distribution

See `assets/NOTICE.md` and the complete licenses in `assets/licenses/`. They are
embedded alongside the data. Preserve these notices and license files when
redistributing the extracted dataset or packaging the application. The Loyalsoldier
repository declares GPL-3.0; component domain licenses and the GeoIP
CC-BY-SA-4.0/GPL-3.0 declarations are retained in the notice. The selected CN IPv4
input is gaoyifan/china-operator-ip (MIT), replacing the former DB-IP provenance.
The reused Xray matcher is MPL 2.0. Filtering and
format transformations are documented above; no upstream endorsement is implied.

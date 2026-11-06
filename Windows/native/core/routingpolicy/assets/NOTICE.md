# Routing data attribution

This notice covers the BlueGrass filtered data derived from
[Loyalsoldier/v2ray-rules-dat release 202610232206](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202610232206).
The release tag resolves to repository commit
`b8ee45341f4995e9bcfd5babe667412841a43ffa`.

| Original upstream asset | SHA-256 |
| --- | --- |
| [geosite.dat](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/202610232206/geosite.dat) | `9cb5ab39ce601ad17d4077909b4d4e943c212af7baa658a612adc64e1c201ca4` |
| [geoip.dat](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/202610232206/geoip.dat) | `a6271d3c96361d96156e6d3f7cf1550ef21b00d2f951deb772f464db91e89ac5` |

These identify the original downloads, not the filtered output files. The
repository declares GNU GPL version 3 in its
[LICENSE at the release commit](https://github.com/Loyalsoldier/v2ray-rules-dat/blob/b8ee45341f4995e9bcfd5babe667412841a43ffa/LICENSE).
The complete text is retained in
[LICENSE-Loyalsoldier-v2ray-rules-dat-GPL-3.0.txt](licenses/LICENSE-Loyalsoldier-v2ray-rules-dat-GPL-3.0.txt).
Component declarations and notices are retained below; the combined data must
not be described simply as the former MIT-only geosite bundle.

## Domain data

The pinned upstream
[README](https://github.com/Loyalsoldier/v2ray-rules-dat/blob/b8ee45341f4995e9bcfd5babe667412841a43ffa/README.md)
and [build workflow](https://github.com/Loyalsoldier/v2ray-rules-dat/blob/b8ee45341f4995e9bcfd5babe667412841a43ffa/.github/workflows/run.yml)
identify the following sources and transformations for the selected categories:

- **V2Fly / V2Ray domain-list-community:** the base domain collection, MIT,
  copyright (c) 2018-2019 V2Ray. Retained text:
  [LICENSE-geosite-MIT.txt](licenses/LICENSE-geosite-MIT.txt).
  License snapshot: `9cad4889b6f1c51bd84cb8a0d95dbc1a992251a4`.
  Its license bytes match the previously retained V2Fly MIT text exactly.
- **Loyalsoldier/domain-list-custom:** the builder and generated custom lists,
  MIT, copyright (c) 2020 Loyalsoldier. Its generated `cn.txt` and
  `geolocation-!cn.txt` feed the selected categories; the rules-dat workflow also
  adds and removes entries using its own `hidden` branch lists. Retained text:
  [LICENSE-Loyalsoldier-domain-list-custom-MIT.txt](licenses/LICENSE-Loyalsoldier-domain-list-custom-MIT.txt).
  License snapshot: `efacb51b8950ae673ebb6dcb9e7ecdd1decb1b6d`.
- **felixonmars/dnsmasq-china-list:** `accelerated-domains.china.conf` contributes
  to `CN`; `apple.china.conf` and `google.china.conf` contribute to
  `GEOLOCATION-!CN`. The declared license is WTFPL version 2, copyright
  (c) 2013 Felix Yan. Retained text, including the upstream copyright notice:
  [LICENSE-dnsmasq-china-list-WTFPL-2.0.txt](licenses/LICENSE-dnsmasq-china-list-WTFPL-2.0.txt).
  License snapshot: `eb9dc5e02c20f9dbcb81e2f868c6cbfc5fc1727f`.
- **GFWList:** domains contribute to `GEOLOCATION-!CN`. The decoded upstream
  `gfwlist.txt` header points to LGPL version 2.1, and the repository supplies
  the full text in `COPYING.txt`. Retained text:
  [LICENSE-GFWList-LGPL-2.1.txt](licenses/LICENSE-GFWList-LGPL-2.1.txt).
  License snapshot: `8b765c742ae7b57ef3dde1e41b49b294c182c922`.
- **cokebar/gfwlist2dnsmasq:** the upstream workflow uses this GPL version 3
  converter to extract GFWList domains. Its license is retained as build
  provenance in
  [LICENSE-gfwlist2dnsmasq-GPL-3.0.txt](licenses/LICENSE-gfwlist2dnsmasq-GPL-3.0.txt),
  snapshot `202e52fb5ea2260abc9f162b3a78e58ec9187005`. This records an upstream
  tool; this sidecar does not bundle that converter, and its license is not used
  to replace GFWList's own data license.

BlueGrass changes: retain only `CN` and `GEOLOCATION-!CN`; remove attributes
after category selection; deduplicate identical matcher type/value pairs, sort,
and re-encode deterministic protobuf while preserving matcher types and values.
Other categories, including `CATEGORY-ADS-ALL` and `WIN-*`, are not selected.
This notice inventories the selected subset, not the entire upstream geosite
download and all of its other categories.

## CN IPv4 data

The rules-dat release downloads `geoip.dat` from **Loyalsoldier/geoip**. That
project's [README](https://github.com/Loyalsoldier/geoip/blob/084eb113e458b82374919dabefbc49cd2c2dd079/README.md)
declares **CC-BY-SA-4.0 and GPL-3.0**, with separate `LICENSE` and `LICENSE-GPL`
texts. Both declarations are retained; they are not rewritten as a choice of
alternative licenses. Retained texts:

- [LICENSE-Loyalsoldier-geoip-CC-BY-SA-4.0.txt](licenses/LICENSE-Loyalsoldier-geoip-CC-BY-SA-4.0.txt)
- [LICENSE-Loyalsoldier-geoip-GPL-3.0.txt](licenses/LICENSE-Loyalsoldier-geoip-GPL-3.0.txt)

The inspected [config.json](https://github.com/Loyalsoldier/geoip/blob/084eb113e458b82374919dabefbc49cd2c2dd079/config.json)
first loads MaxMind country data, removes the entire `cn` list, then adds
`gaoyifan/china-operator-ip`'s `ip-lists/china.txt` as CN IPv4 and `china6.txt`
as CN IPv6. This agrees with the rules-dat release README's explicit CN source
statement. The non-CN country lists and IPv6 are not included in this filtered
CN IPv4 sidecar.

**gaoyifan/china-operator-ip** declares the MIT License in its
[README](https://github.com/gaoyifan/china-operator-ip/blob/b2b8edc0ddd9cf2bc638ec77587301bbf84b8db9/README.md)
and [LICENSE](https://github.com/gaoyifan/china-operator-ip/blob/b2b8edc0ddd9cf2bc638ec77587301bbf84b8db9/LICENSE),
copyright (c) 2017 Yifan Gao. Retained text:
[LICENSE-china-operator-ip-MIT.txt](licenses/LICENSE-china-operator-ip-MIT.txt).
This is the current provider's project declaration, not a separately discovered
license attached to each generated prefix.

BlueGrass changes: retain CN IPv4 CIDRs only, mask and sort them, remove duplicate
and contained prefixes and IPv6, and re-encode deterministic protobuf. These
changes are made by the BlueGrass project. The derived GeoIP data retains the
upstream CC BY-SA 4.0 terms, with the GPL notices and underlying MIT attribution
above also preserved.

The former DB-IP provenance statement does not describe this replacement CN
subset. The old `LICENSE-DB-IP-CC-BY-4.0.txt` and
`LICENSE-geoip-CC-BY-SA-4.0.txt` remain as historical files; their presence does
not identify a source of the replacement data. The newly named Loyalsoldier
CC BY-SA text records the current attribution even though its license bytes
equal the previously retained generic CC BY-SA text.

## Xray implementation

The matcher and protobuf implementation remains the existing Xray-core
dependency `v1.260327.1-0.20261017152356-92ccc939aaf4`, under Mozilla Public
License 2.0. Source:
[XTLS/Xray-core common/geodata](https://github.com/XTLS/Xray-core/tree/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/common/geodata).
Retained text: [LICENSE-Xray-MPL-2.0.txt](licenses/LICENSE-Xray-MPL-2.0.txt).
This implementation attribution is separate from the data-source declarations.

## Pinned license texts

Checked on 2026-10-24 against public primary repositories. Each link below is a
GitHub Contents API URL pinned to a full commit. SHA-256 is over the decoded file
bytes, not the JSON response. The local copies preserve the complete upstream
text, including original notices and line endings.
License labels record the versions declared by the repositories; no unverified
`-only` or `-or-later` qualifier or combined SPDX expression is inferred.

| Local file under `licenses/` | Pinned source | SHA-256 |
| --- | --- | --- |
| `LICENSE-Loyalsoldier-v2ray-rules-dat-GPL-3.0.txt` | [Loyalsoldier/v2ray-rules-dat LICENSE](https://api.github.com/repos/Loyalsoldier/v2ray-rules-dat/contents/LICENSE?ref=b8ee45341f4995e9bcfd5babe667412841a43ffa) | `3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986` |
| `LICENSE-geosite-MIT.txt` | [v2fly/domain-list-community LICENSE](https://api.github.com/repos/v2fly/domain-list-community/contents/LICENSE?ref=9cad4889b6f1c51bd84cb8a0d95dbc1a992251a4) | `b9d84a22870d3f21c91a4c6e410c9cc51d00902f5233ad0c84011479244bf7d2` |
| `LICENSE-Loyalsoldier-domain-list-custom-MIT.txt` | [Loyalsoldier/domain-list-custom LICENSE](https://api.github.com/repos/Loyalsoldier/domain-list-custom/contents/LICENSE?ref=efacb51b8950ae673ebb6dcb9e7ecdd1decb1b6d) | `35f18e0331a1ecd1835400c50e3b367c2ce09f6c13d91c4a0f3cb11f71d3bbc3` |
| `LICENSE-dnsmasq-china-list-WTFPL-2.0.txt` | [felixonmars/dnsmasq-china-list LICENSE](https://api.github.com/repos/felixonmars/dnsmasq-china-list/contents/LICENSE?ref=eb9dc5e02c20f9dbcb81e2f868c6cbfc5fc1727f) | `f9e00539c44942d94e3c6d20f0c90e63e810ea181b10f16a252c93e4ea5cefd7` |
| `LICENSE-GFWList-LGPL-2.1.txt` | [gfwlist/gfwlist COPYING.txt](https://api.github.com/repos/gfwlist/gfwlist/contents/COPYING.txt?ref=8b765c742ae7b57ef3dde1e41b49b294c182c922) | `dc626520dcd53a22f727af3ee42c770e56c97a64fe3adb063799d8ab032fe551` |
| `LICENSE-gfwlist2dnsmasq-GPL-3.0.txt` | [cokebar/gfwlist2dnsmasq LICENSE](https://api.github.com/repos/cokebar/gfwlist2dnsmasq/contents/LICENSE?ref=202e52fb5ea2260abc9f162b3a78e58ec9187005) | `589ed823e9a84c56feb95ac58e7cf384626b9cbf4fda2a907bc36e103de1bad2` |
| `LICENSE-Loyalsoldier-geoip-CC-BY-SA-4.0.txt` | [Loyalsoldier/geoip LICENSE](https://api.github.com/repos/Loyalsoldier/geoip/contents/LICENSE?ref=084eb113e458b82374919dabefbc49cd2c2dd079) | `5e436ff8ffbb77d8607220e9bce20c8915d860010feeb6c1ebef5a85688e9b39` |
| `LICENSE-Loyalsoldier-geoip-GPL-3.0.txt` | [Loyalsoldier/geoip LICENSE-GPL](https://api.github.com/repos/Loyalsoldier/geoip/contents/LICENSE-GPL?ref=084eb113e458b82374919dabefbc49cd2c2dd079) | `3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986` |
| `LICENSE-china-operator-ip-MIT.txt` | [gaoyifan/china-operator-ip LICENSE](https://api.github.com/repos/gaoyifan/china-operator-ip/contents/LICENSE?ref=b2b8edc0ddd9cf2bc638ec77587301bbf84b8db9) | `8f4d03a581484143945a9fac471c3b669d5aada68361fd1f25886c3ddbdd786a` |
| `LICENSE-Xray-MPL-2.0.txt` | [XTLS/Xray-core LICENSE](https://api.github.com/repos/XTLS/Xray-core/contents/LICENSE?ref=92ccc939aaf4f8965f369cf0b142e005aeaa11d5) | `1f256ecad192880510e84ad60474eab7589218784b9a50bc7ceee34c2b91f1d5` |

## Verification boundaries

Verified directly: the release tag's commit; the two asset digests advertised by
the public GitHub release API; the pinned README/workflow/config source
statements; every license text and its local SHA-256; and the existing Xray
module version/import. The V2Fly MIT and Xray MPL files were checked without
changing their bytes. Downloading and hashing the original data and generating
the filtered outputs are separate steps performed by the data generator.

The license commits above are inspection snapshots. Except for the rules-dat
release commit and the application's Xray dependency pin, they are **not claimed
to be the exact revisions consumed by the release build**. In particular, the
V2Fly license snapshot is newer than the release. The upstream workflow reads
moving default, `release`, `hidden`, and `ip-lists` branches; the rules-dat tag
alone does not lock those inputs. The source paths for the selected categories
are established by the workflow and declarations, but the exact ancestry of
every emitted domain/prefix has not been independently reconstructed. Excluding
the former DB-IP provenance from this CN subset follows that documented CN
replacement path, not a byte-level reconstruction of every upstream input.

`metadata.json` records the generator's source/output hashes when the matching
data update is generated; this attribution update alone does not establish that
the checked-in binaries or metadata have already been regenerated. Preserve
this notice and the complete license texts when redistributing the data.
These data are supplied without warranty; none of the upstream projects or data
providers endorses this application or its routing decisions.

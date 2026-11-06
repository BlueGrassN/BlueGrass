# ADR 0031：默认允许局域网，按 RFC 地址用途走本地路由

- 状态：默认行为已接受；地址分类及平台路径按下述设计实施，运行验证待完成。
- 决策日期：2026-10-11，Asia/Hong_Kong。
- 对应事项：全项目复核 C06、旧 F05/F13；TUN/VPN 局域网访问、DNS 与 FakeIP 规则。
- 确认依据：用户选择默认允许局域网访问，并要求 RFC 规定的局域网地址尽量按本地路由处理。

## 决定

1. 新安装的 `allowLan` 默认开启，设置页允许用户关闭并持久保存选择；后续启动、更新和远程配置不覆盖已保存选择。
2. 允许的本地地址使用本机原有的本地/底层路由，不发往所选公网中转。按对应接口、最长前缀、路由优先级和 IPv6 scope 选择路径，不统一强制到某一张物理网卡。
3. “本地路由”排除本产品自身创建的 TUN/VPN 路径，避免循环；切网后重新核实可用本地接口和路由。没有可用本地路径时返回不可达，不回退到远端中转。
4. 局域网直连与中转的 UDP/IPv6 能力独立。中转不支持 UDP 或 IPv6 时，仍可按本开关访问具备本地路径的 LAN UDP/IPv6；不支持的公网业务继续按原规则阻断。
5. 回环、必要网络控制、本地广播/多播、普通 LAN 单播、业务 DNS 和 FakeIP 分别处理。RFC 的特殊用途地址注册表不是“全部 LAN 直连”清单，不能直接用一个“非公网地址”判断放行所有条目。

## 本地单播地址范围

下表中的标准用途来自 RFC/IANA；“采用本地路由”是本产品按用户要求选择的策略，不表示 RFC 规定所有代理软件必须采用同一实现。

| 范围 | 标准用途 | 本产品处理 |
| --- | --- | --- |
| `10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16` | RFC 1918 私有 IPv4 | `allowLan=true` 时按原有本地/底层路由访问，覆盖三段完整范围；无路由不转交公网中转。 |
| `fc00::/7` | RFC 4193 IPv6 ULA；通常本地分配使用 `fd00::/8`，L=0 范围未作为普通本地分配定义 | 作为 ULA 匹配范围处理，先排除本产品实际使用的虚地址/FakeIP 地址池，再按本地路由访问；不把 `fc00::/8` 当作建议给真实 LAN 分配的池。 |
| `169.254.0.0/16`、`fe80::/10` | IPv4/IPv6 链路本地地址 | 仅在对应链路访问，保留接口/scope 信息；不能经网关转发到其他链路。多网卡时不猜接口。 |
| `100.64.0.0/10` | RFC 6598 Shared Address Space，通常用于运营商共享地址 | 作为额外兼容项纳入本地/底层路由策略，便于访问实际采用该地址的本地或运营商网络；不将其称为 RFC 1918 私网。 |
| `127.0.0.0/8`、`::1/128` | 本机回环 | 留在本机，不交给远端中转，也不通过物理网口发送；必要回环不依赖 LAN 开关。 |
| 本机实际接口的直连前缀 | 可能包括本地 LAN 使用的 IPv6 全局地址等 | `allowLan=true` 时识别实际本地直连前缀并保留其本地路径；不把整个 IPv6 全局地址空间或任意公网静态路由都豁免。 |

依据：[RFC 1918 §3](https://www.rfc-editor.org/rfc/rfc1918.html#section-3)、[RFC 4193 §3.1](https://datatracker.ietf.org/doc/html/rfc4193#section-3.1)、[RFC 3927 §2.7](https://www.rfc-editor.org/rfc/rfc3927.html#section-2.7)、[RFC 4291 §2.5](https://www.rfc-editor.org/rfc/rfc4291.html#section-2.5)、[RFC 6598](https://www.rfc-editor.org/rfc/rfc6598.html)。

## 广播、多播与其他特殊用途

- `255.255.255.255/32` 和实际接口的 IPv4 子网广播按本链路语义处理；不启用跨接口定向广播转发。DHCP、邻居发现等必要网络控制沿用既有受约束规则。
- IPv4 本地控制多播 `224.0.0.0/24` 及管理域多播 `239.0.0.0/8` 按接口和既有 scope 边界处理。IPv6 `ff00::/8` 必须解析 scope，接口/链路本地以及确有相应本地配置的其他局部作用域留在原作用域；不整体放行全局多播。
- `.local` 的 mDNS 使用本链路 `224.0.0.251:5353` / `[ff02::fb]:5353`，按 LAN 开关和平台能力处理，不当作普通公网业务 DNS。发现包不能跨接口泛洪；API 26～当前 Android 的设备发现能力需实测。
- `0.0.0.0`、`::` 等未指定地址按系统协议语义处理，不当作普通 LAN 目标；IPv4-mapped 地址按其所表示的 IPv4 地址分类，不能把整个 `::ffff:0:0/96` 当作 LAN 例外。
- 文档示例、基准测试、丢弃、转换及其他保留地址各有用途，例如 `192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`、`2001:db8::/32`、`198.18.0.0/15`、`2001:2::/48`、`100::/64`、`64:ff9b::/96`。它们不因“非公网/特殊用途”就自动成为 LAN 直连范围；实际测试网络等额外用途须有明确本地路由配置并避开本产品虚地址。

依据：[RFC 2365](https://www.rfc-editor.org/rfc/rfc2365.html)、[RFC 7346](https://www.rfc-editor.org/rfc/rfc7346.html)、[RFC 6762](https://www.rfc-editor.org/rfc/rfc6762.html)、[IANA IPv4 特殊用途表](https://www.iana.org/assignments/iana-ipv4-special-registry/iana-ipv4-special-registry.xhtml)、[IANA IPv6 特殊用途表](https://www.iana.org/assignments/iana-ipv6-special-registry/iana-ipv6-special-registry.xhtml)。

## DNS、FakeIP 与规则顺序

1. 本产品 TUN 地址、虚拟 DNS 地址及实际 FakeIP 池先按专用规则识别，不作为真实 LAN 目标走物理网络。现有候选 `fc00::/18` 包含于 ULA `fc00::/7`，因此 FakeIP 映射恢复必须早于 ULA 直连；`198.18.0.0/15` 的基准测试用途也不能使 FakeIP 流量绕过内核。
2. 普通应用的 TCP/UDP 53 查询继续由内核接管，不能因目标是私有地址上的路由器而直接放行公网业务查询。入口 DNS 的受约束查询、mDNS 和必要网络控制分别使用已有专用路径。
   后续 [D-32](0032-local-domain-dns.md) 已确认 `.lan`、`home.arpa` 等明确内网名称由受控原生层向相应本地解析器查询；公网名称不获得该例外，本地与公网解析失败均不互相回退。
3. 普通 LAN 单播以“进入内核分类，再按原有本地路径直连”为统一设计基线。只有在平台能同时保证 DNS 接管、虚地址优先和保护规则时，才可对具体流量使用系统路由排除；不把整段 IP 排除当作已经解决端口 53 的证明。
4. 完成专用处理后，允许的真实本地地址走本地路由；其余公网业务走所选资源或在保护阻断时拒绝。关闭 LAN 开关时停止普通 LAN 直连例外，保留必要回环、控制面及网络配置流量。
5. 启动及切网时检查 TUN/FakeIP 池与实际本地地址和路由的冲突，必要时重新选择无冲突的内部地址池并同步映射/路由；不能用虚地址覆盖真实 LAN，旧 FakeIP 映射也不能在池切换后误作真实地址直连。具体池选择和恢复事务由原生实现验证。

按 [ADR 0049](0049-xray-only-four-platforms.md)，当前使用固定 Xray 的[路由配置](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/router.go)及平台 DNS/TUN 适配实现上述顺序。旧 sing-box 的 `hijack-dns` / `ip_is_private` 不能直接写入 Xray 配置；专用 DNS 规则、虚地址隔离和系统路由仍须逐平台验证。

## 平台落点与验收边界

- Windows TUN：内核负责分类与本地出站，WFP 保留符合该地址表的本地例外、业务 DNS 限制及虚地址隔离；本地出口选择不能重新进入本产品 TUN。系统代理模式沿自身能力执行回环/本地例外，不宣称具备全局过滤能力。
- Android：普通 LAN 单播由 VPN/内核分类后，经 `protect` 及正确底层网络的连接发送；不用整段 `excludeRoute` 或补集路由作为默认实现。回环、链路作用域、广播和多播依照系统能力单独验证。
- Android 源码中的 `excludeRoute(IpPrefix)` 只接受 IP 前缀，并按最长前缀匹配；据此不能表达“同一地址的普通 LAN 流量放行但端口 53 接管”。[AOSP VpnService 源码](https://raw.githubusercontent.com/aosp-mirror/platform_frameworks_base/master/core/java/android/net/VpnService.java)
- 始终开启 VPN/lockdown、系统版本和厂商行为可能影响本地可达性；不得关闭系统保护来实现 LAN 例外。S7 验证开关组合、IPv6 scope 与多网卡出口，结果未取得前不宣称全部组合可用。
- 本地例外在已允许 LAN 时不依赖中转握手成功；公网业务的出口验证与保护门槛保持 D-07/D-29。LAN 名称按 D-32 分流，本决定不整体放开路由器 DNS。

## 验证要求

- 覆盖三段完整 RFC 1918、ULA、共享地址、链路本地、回环与实际本地直连前缀；验证多网卡、接口切换、IPv6 scope、无本地路由和路由重叠，LAN 流量不进入公网中转。
- 中转不支持 UDP/IPv6 时，本地已允许流量仍按本地能力工作，公网不支持流量不直连。
- 覆盖 FakeIP 与 ULA/测试地址重叠：假地址在物理接口与远端目标上均不作为真实地址发出；还原后的公网业务仍走代理。
- 普通应用明确向 LAN 路由器发送 TCP/UDP 53 公网查询时仍被接管；mDNS 只在正确本地作用域工作。
- Windows 与 Android 分别验证 LAN 开/关、正常连接/保护阻断、最低系统版本、API 33 前后、lockdown 和进程/网络恢复；“公网零泄漏”计数剔除明确允许的本地与控制流量，不把未知流量临时加入白名单。
- 本轮仅完成标准核对和设计同步，未运行客户端、抓包、修改系统路由或部署规则；F05 的运行验证仍未关闭。

## 关联文档

- [D-04：入口 DNS](0004-entry-dns-policy.md)
- [D-06：首轮 Beta 网络保护](0006-first-beta-network-protection.md)
- [D-29：固定出口比对](0029-fixed-expected-exit-ip.md)
- [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)
- [全项目复核](../superpowers/reviews/2026-10-11-bluegrass-project-reaudit.md)

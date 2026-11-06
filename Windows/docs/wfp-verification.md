# Windows WFP 保护实现与验收边界

更新：2026-10-12。范围：`core/winprotect`、`winservice` 组合层及新增的 `serviceipc` 更新助手只读鉴权入口。

本包已实现真实 Windows WFP API 调用、事务替换、持久保护意图和独立 WFP 修复入口；本次只运行无防火墙写入的策略、生命周期、ABI 测试与编译。**没有安装规则、调用 `New`/`Repair`、修改本机防火墙/路由/DNS，尚未完成 Windows 提权运行、重启或抓包验收，不能据此通过对外 Beta1 的零泄漏门槛。** SMS 不属于本包范围。

设计依据：[补充设计 §8.2–8.3](../../docs/superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)、[ADR 0006](../../docs/adr/0006-first-beta-network-protection.md)、[ADR 0031](../../docs/adr/0031-default-local-network-routing.md)、[ADR 0032](../../docs/adr/0032-local-domain-dns.md)。本次明确要求覆盖了补充设计中“没有保护意图时自动清理残留”的旧候选：当前实现保留阻断并进入需要显式修复的状态。

## 服务接入契约

```go
func New(serviceExe, uiExe string) (*Manager, error)
func (m *Manager) ArmDNS(ownerSID, accountKey, resourceID string,
    dns []netip.AddrPort, allowLAN bool) error
func (m *Manager) Arm(ownerSID, accountKey, resourceID string,
    entry netip.AddrPort, dns []netip.AddrPort, allowLAN bool) error
type DNSEndpoint struct {
    Endpoint netip.AddrPort
    Transport string // 仅 "tcp" 或 "udp"；端口必须非零
}
func (m *Manager) ArmDNSWithPolicy(ownerSID, accountKey, resourceID string,
    dns []DNSEndpoint, allowLAN bool) error
func (m *Manager) ArmWithDNSPolicy(ownerSID, accountKey, resourceID string,
    entry netip.AddrPort, dns []DNSEndpoint, allowLAN bool) error
type EntryPolicy struct { TCP, UDP, AuthenticatedSOCKSRelay bool }
func (m *Manager) ArmWithEntryPolicy(ownerSID, accountKey, resourceID string,
    entry netip.AddrPort, transports EntryPolicy, dns []DNSEndpoint, allowLAN bool) error
func (m *Manager) ResumeLogicalSession(ownerSID string, key engine.ResourceKey) (string, error)
func (m *Manager) BindLogicalSession(ownerSID string, key engine.ResourceKey, id string) error
func (m *Manager) SwitchLogicalSession(oldKey, newKey engine.ResourceKey, newID string) error
func (m *Manager) BeginRuntime(ownerSID string, key engine.ResourceKey, id string) error
func (m *Manager) BeginMaintenance(ownerSID string) (SessionCheckpoint, error)
func (m *Manager) ConfirmOwnedCleanup(receipt SessionCheckpoint) error
func (m *Manager) SessionCheckpoint() (SessionCheckpoint, bool, error)
func (m *Manager) CleanupRequired() bool
func (m *Manager) ArmLocalDNS(ownerSID string, dns []DNSEndpoint, allowLAN bool) error
func (m *Manager) BeginLocalRuntime(ownerSID string) error
func (m *Manager) AllowLocalTunnel(ownerSID string, luid uint64) error
func (m *Manager) UpdateControlDNS(dns []DNSEndpoint) error
func (m *Manager) UpdateControlDNSForOwner(ownerSID string, dns []DNSEndpoint) error
func (m *Manager) AllowUpdateHelper() error
func (m *Manager) AllowTunnel(luid uint64) error
func (m *Manager) AllowUDPRelay(endpoint netip.AddrPort) error
func (m *Manager) Block() error
func (m *Manager) Release() error
func (m *Manager) Active() bool
func (m *Manager) OwnerSID() string
func (m *Manager) Authorized(ownerSID, accountKey, resourceID string) bool
func (m *Manager) RecoveryRequired() bool
func (m *Manager) Close() error
func Repair() error
```

- `New` 只支持 Windows amd64/arm64，要求 LocalSystem 或提升后的管理员令牌。传入本机安装目录内服务、GUI 的绝对可执行文件路径；服务与 GUI 应使用独立可执行文件，否则 GUI TCP443 例外也将授予服务。安装路径、发布者和 IPC 调用方校验由主服务完成。初始化可能执行保护恢复，禁止在普通单元测试中调用。
- 服务从可信 IPC 调用方令牌取得 SID，禁止直接采用 JSON 声称的 SID。`Arm` 接收不含秘密的账户/资源标识和已由可信原生代码解析的端点；不处理登录凭据、令牌或域名解析。
- 首次连接需要解析入口名称时，先从 `entrydns.Policy.Effective(usableDNS(SystemNetwork.EntryDNS))` 取得候选，按候选准确的 IP、默认后的端口和 TCP/UDP 调用 `ArmDNSWithPolicy`，成功后才调用共享 `entrydns.ResolvePhysical`。自动模式最多8个物理 DNS 端点，再依次追加 `1.1.1.1:53`、`223.5.5.5:53` 并去重，每个端点依次 UDP/TCP，最多20个传输候选；自定义模式不追加系统候选或公共后备，总解析预算5秒。引导与完整 Arm 共用校验、先保存意图后安装 WFP 的事务路径；没有入口放行，也不允许 TUN 或 UDP relay。字面量入口不要求存在自动 DNS，仍以空 DNS 引导阻断。旧 `ArmDNS`/`Arm` 接口兼容保留双传输行为。
- `ArmDNS` 成功后保持 `armed=false`、`rebindReady=true`。解析完成后，同一 SID、账户、资源可直接调用完整 `Arm`，无需中间 `Block`；不能利用 bootstrap 更换身份绑定。已有已武装会话须先成功 `Block` 才能进入新的 bootstrap。bootstrap 失败时禁止继续解析或直接尝试放行业务。
- `Arm` 先原子保存保护意图，再事务安装阻断规则。服务组合先 Bind 完整逻辑会话，Arm 成功后还必须提交 `BeginRuntime` 清理日志，随后才能创建 TUN、开始受控探测。普通业务转发保持关闭。
- 服务依据 `protocolconfig.Requirements` 构造 `EntryPolicy`：SOCKS 入口 TCP，认证后才可添加精确 relay；Shadowsocks 为精确 TCP+UDP 入口；Hysteria2/TUIC 为精确 UDP 入口；其他协议使用其明确声明的 TCP/UDP。非 SOCKS 不传递 relay 回调。旧 `Arm`/`ArmWithDNSPolicy` 兼容接口的入口按 SOCKS TCP处理，DNS双传输不等于入口UDP许可。
- 服务完成当前登录代次、账户、资源资格、访问与出口验证后，才可调用 `AllowTunnel`。本包校验 LUID 对应已启用、IfType=53、名称为 `BlueGrass` 或 `BlueGrass-TUN` 的接口；仅此不能证明出口或业务资格。当前 tunengine 使用 `BlueGrass` 名称。
- 初始 Probe 使用 TCP，不提前添加 UDP relay。完整 `Arm` 和 `AllowTunnel` 成功之后，当前已认证 SOCKS `UDP ASSOCIATE` 响应中的 relay 地址须先经 `AllowUDPRelay` 成功授权，再创建 UDP socket。winservice 给 `Config.AllowUDPRelay` 传入捕获本次会话 token 的闭包，不能直接绑定 Manager 方法。2026-10-20 实机修正：调用时再次核实 TUN LUID 存活，接受认证响应中的精确公网 IPv4 及非零端口，允许与 TCP 入口不同；仍拒绝私网、CGNAT、虚拟池、IPv6、IPv4-mapped、未指定地址及端口0。每个会话最多64个活跃端点，同端点的不同关联通过引用计数共同持有许可，最后一个关联关闭才回收。证据见[管理员验收](elevated-tun-acceptance.md)。
- relay 规则仅为服务可执行文件、出站 UDP、精确 IP/端口，不持久化。`Block`、新 `Arm`/`ArmDNS`、启动恢复、`Release` 清除所有 relay；重新放行 TUN 不恢复旧端点。添加事务失败返回错误，不把新端点加入当前策略，并使会话进入恢复状态；此时旧事务规则可能还在，主服务必须关闭业务门再恢复。DNS 原有精确端点的 TCP/UDP 放行与 relay 分开处理。
- `AllowTunnel` 不能将已放行 LUID 直接换成另一个 LUID。切换资源、端点、DNS、LAN 策略、底层接口或 TUN 时先关闭业务门，再成功 `Block`、重新 `Arm`、重新验证，最后放行。`Block` 成功也不自动恢复此前会话的业务授权。
- 同一保护意图只归属一个 SID。v2 日志同时固定完整 ResourceKey（环境、数据源、账户、类型、资源ID）和 logicalSessionID，普通 Bind/Block、升级或重启不能替换。显式 `SwitchLogicalSession` 可在同一SID/环境/数据源/账户内切换资源并产生新逻辑ID，始终保留保护；跨上述归属则须显式解除。重启若有清理未确认，须先执行精确自有适配器修复并持久确认，再重新 bootstrap/Arm/验证。旧 v1 或缺少完整逻辑会话的记录迁移为 `legacy` 阻断，不编造 logicalSessionID。
- `Authorized` 仅是 SID/账户/资源的兼容预检；完整键及逻辑ID由 `ResumeLogicalSession` / `BindLogicalSession` 与 Prepare 校验，不代表已获得业务证明。网络事件、登录代次、迟到异步结果及资格失效仍由服务状态机控制。
- `Release` 无 SID 参数，必须由服务先核实可信调用者 SID 等于 `OwnerSID()`，停止内核并删除 TUN，再显式调用；不要求业务登录成功。未知归属残留无法通过 `Release` 或 `Arm` 被新用户接管，必须使用管理员 `Repair`。
- `Close` 只关闭句柄，不删除规则、不清除意图。正常用户退出是否解除保护由服务显式选择 `Release`，不能依赖 `defer Close()`。

### 状态与错误语义

`Active()` 表示 **内存中的保护意图仍为真，或已提交的规则仍存在**，不是“已经抓包证明阻断”的布尔值。界面必须结合操作错误和 `RecoveryRequired()` 显示恢复状态。

| 场景 | 管理器处理 | 主服务要求 |
| --- | --- | --- |
| 首次 `Arm`/`ArmDNS` 保存意图失败 | 不安装规则，返回错误 | 不开始入口解析/TUN/业务，不显示已受保护 |
| 意图保存成功、WFP 激活失败 | 保留意图，`Active=true`、恢复状态为真 | 关闭业务门，报告失败；重试阻断或显式恢复 |
| `AllowTunnel`/`AllowUDPRelay`/`Block`/删除规则事务失败 | 撤销本次会话授权，恢复状态为真；事务回滚可能保留旧 TUN/relay 例外 | 关闭内核业务门；不能将错误解释为“成功阻断” |
| `Release` 删除成功、保存 inactive 记录失败 | 返回错误，保留原归属和意图，`Active=true`、恢复状态为真；此时规则可能已经不存在 | 显示待恢复，允许原 SID 重试；不能显示解除成功 |
| 完整 `Release` 成功 | 规则删除、inactive 记录落盘，`Active=false`，清除原归属 | 再按原有归属协议处理系统代理 |
| 有保护意图的重启 | 保留原 SID、完整资源键、logicalSessionID 和清理标记，保存恢复状态，替换成基础阻断；无入口、DNS、LAN、业务/本地 TUN 或 relay 会话例外 | 若清理待确认，先精确修复并持久确认；原 SID 可显式解除，无需业务登录；继续连接还须完整绑定与新证明 |
| 缺失/无效意图但有自有 WFP 对象 | 重建阻断并保存未知归属恢复意图，不默默删规则 | 提示管理员修复 |
| 不可读或损坏的意图 | 按未知归属保守恢复，不能据此认领旧 SID | 修复状态目录/权限或运行管理员修复 |
| 启动时保存恢复记录失败 | 仍尝试撤销旧 TUN 例外；返回初始化错误 | 服务不得就绪，报告状态持久化错误 |

恢复标记在启动时持久化。单次操作失败产生的内存恢复标记可能尚未写盘；此前已提交的 `protectIntent=true` 使下一次启动必然重新进入恢复，而不会自动恢复旧会话放行。

## WFP 实现

直接使用 `golang.org/x/sys/windows` 加载 `fwpuclnt.dll`，使用公开 WFP API。没有读取第三方库私有句柄，也没有引入新模块依赖。最小 ABI 定义参考缓存中的 `github.com/tailscale/wf`，附带 [LICENSE.wf](../native/core/winprotect/LICENSE.wf)，并与 Windows SDK 10.0.26100.0 结构和常量核对。

| 对象 | 固定标识/处理 |
| --- | --- |
| Provider | `{ec26f58b-7149-40ea-8d68-17be93af3bd1}`，持久，无 ServiceName 绑定 |
| Sublayer | `{e0da41ca-0718-41bd-96ad-cbe277ad6a0f}`，持久，provider 必须匹配 |
| 会话 | 非动态；关闭句柄不自动撤销规则 |
| 对象 ACL | SYSTEM、Administrators，禁止普通用户修改 |
| 删除范围 | 逐个枚举实际 WFP layer，包含 boot-time/disabled；仅删除 provider 和 sublayer 同时匹配的过滤器，再删除自有 sublayer/provider |
| 更新 | `FwpmTransactionBegin0` → 删除旧自有过滤器 → 加入新规则 → `FwpmTransactionCommit0`；失败调用 `FwpmTransactionAbort0` 并保留错误 |

枚举或删除失败会使整个事务失败，不清理其他安全软件的对象。外部对象若引用本 provider/sublayer 导致删除受阻，则报告错误，不扩大删除范围。对象添加失败或事务中止失败同样报告错误；不把部分 API 调用成功当成完整保护。

### 规则范围与优先级

运行规则位于 `ALE_AUTH_CONNECT_V4/V6` 和 `ALE_AUTH_RECV_ACCEPT_V4/V6`。权重使用实际 `FWP_UINT64` 值。每一方向、每一 IP 家族均有默认阻断。

| 优先级 | 例外/规则 | 持久性和边界 |
| --- | --- | --- |
| 250 | 系统标记的 loopback | 持久；不依赖 LAN 开关 |
| 240 | 已验证 TUN 的精确 LUID | **不持久**；允许虚地址进入受控 TUN，禁止泛化到接口类型 |
| 230 | 默认和额外产品虚地址池阻断 | 持久；高于任何物理网络例外，涵盖入站远端源和出站目标 |
| 240 | local-only TUN 精确 LUID → TCP/UDP远端53 | **不持久**；仅向独立分类 DNS 运行时送入查询，不能代替业务 TUN 许可 |
| 220 | 服务可执行文件 → 精确入口、DNS 地址及端口 | 入口按协议要求，DNS按显式 transport；local-only服务DNS例外不持久；无任意地址/端口放行 |
| 220 | 服务可执行文件 → 当前入口 IPv4 上的 SOCKS UDP relay 精确端口 | **不持久**，仅出站 UDP；已验证 TUN 存活时才可添加，最多64个 |
| 220 | GUI 及经服务确认的固定 Update 助手可执行文件 → TCP443 | 持久，出站；属于显式控制面例外，IPv4/IPv6 同样适用；助手主机白名单由签名配置执行 |
| 210 | DHCPv4 UDP local68/remote67；DHCPv6 local546/remote547 | DHCPv6 出站仅 ff02::1:2，入站仅 fe80::/10；DHCPv4 依端口元组约束 |
| 210 | ICMPv6 133–136 | 远端限制为 fe80::/10、ff02::/16、::/128；ND/RA 控制用途 |
| 100 | 已允许 LAN，按可用 Ethernet/Wi-Fi 类型接口的精确 LUID | 持久；TCP/UDP 同时排除本地和远端端口53，另有 LAN ICMP |
| 1 | 其余运行流量 | 持久阻断 |

LAN 范围包含 RFC1918、CGNAT、IPv4/IPv6 link-local、ULA、实际接口直连前缀；有界 IPv4 本地/管理域多播、IPv6 interface/link-local scope 多播及广播。不会整体豁免所有“非公网”地址或整个 IPv6 多播范围。本包不改系统路由；正确 scope、路由选择、原生本地出站、网络切换刷新仍需 tunengine/主服务配合。非 Ethernet/Wi-Fi 类型接口保守不获得普通 LAN 例外。

开机规则是额外四条 `BOOTTIME` 无条件阻断，分别位于入站/出站 IPPACKET 的 IPv4/IPv6 layer；不把 `BOOTTIME` 和 `PERSISTENT` 合并在同一过滤器上。早期开机阶段不假设应用身份或可信 TUN 已可用，因此不添加相应开机放行。运行例外在 BFE 的运行规则生效后提供。此设计仍需验证系统版本对这些 layer、对象标志和开机接管的实际支持。

TUN 例外虽然不持久，但使用非动态会话，可能在单独服务崩溃后继续存在到 BFE 重启或显式替换；它只指向旧精确 LUID。持久默认阻断仍存在，下一次 `New` 先删除旧例外。**进程崩溃时内核是否停止转发、旧接口消失及 LUID 是否可能复用，必须联同 tunengine 抓包验证，不能仅凭这些标志宣称零泄漏。**

UDP relay 同样使用非持久、非动态规则：单独进程崩溃不等于自动删除，但它始终受服务 app、认证响应中的精确公网 IPv4 和端口限制，启动恢复会清理。不得将其扩大成整台主机的全部 UDP 放行。

### 固定虚地址排除

默认始终排除以下全部候选，重启后也不依赖外部回调：

```text
198.18.0.0/15      # 同时覆盖 198.18.0.0/16 和 198.19.0.0/16
fc00::/18
172.19.0.0/30
172.31.255.0/30
172.30.255.0/30
10.255.255.0/30
fdfe:dcba:9876::/126
fdde:adbe:ef01::/126
```

这是按集成要求保守预留所有候选池，即使当前会话只使用其中一组，其余组也不会成为物理 LAN 例外。真实 LAN 落在这些小型候选范围内时同样受阻，池冲突处理不能假设未选候选会被本包放行。

额外接口 `SetVirtualPrefixes([]netip.Prefix) error` 仅可在没有保护意图时调用，追加排除且不移除默认池，不持久化。主服务的当前固定候选无需调用它，也不应在 `Arm`/`ArmDNS` 后调用。未来增加池时应同步默认集或在首次激活前配置，不能依赖重启后仍保留本次内存追加值。

### DNS 边界

本包只允许受控服务进程访问明确的 DNS 端点；普通应用的 LAN TCP/UDP53 被 LAN 规则排除。WFP 不识别查询名称，不负责 ADR0032 的 `.lan`/`home.arpa`/mDNS 分类、缓存代次、别名再次分类或本地/公网禁止互相回退。

传入 `dns` 的本地解析器必须已经由可信原生层核实网络归属，发送套接字须由原生层正确绑定底层网络；当前 API 不包含每个解析器的 LUID，WFP 本身不表达该网络绑定。入口 DNS 与本地名称 DNS 的独立来源及 LAN 关闭时撤销本地查询，均由主服务重新生成列表并执行阻断/重设流程。不能把普通业务公网 DNS 加入这些直连端点来绕过规则。

端点拒绝端口0、未指定、多播、回环、IPv4-mapped、带 zone 或任何 link-local 地址；这类系统候选被过滤。自动入口/控制 DNS 仍可尝试上述两个明确公共后备；自定义或本地域名需要 scoped/link-local DNS 时不扩大许可，失败关闭，不能声称 ADR0032 全部网络组合已经支持。`entrydns.Candidate` 的端口0会先由共享包规范化为53，再交给 WFP；WFP 本身没有默认端口。

### 服务 DNS 的物理接口绑定

服务入口、固定 API 主机与更新主机使用 `entrydns.ResolvePhysical`。WFP 端点许可不改变路由，因此解析器不再让 `dns.Client` 自行建立普通套接字。它只在已绑定的连接上执行 DNS 编解码，任一接口验证、绑定、连接失败都只进入策略内的下一个候选，不回退系统解析器或无绑定拨号。

`SelectPhysicalRoute(ctx, destination)` 读取 `GetAdaptersAddresses`、`GetIfEntry2Ex`、`GetBestInterfaceEx`，并对每个已验证物理 LUID 调用受该 LUID 约束的 `GetBestRoute2`。仅接受已启用、已连接的硬件 Ethernet/Wi-Fi/原生 WWAN（IF_TYPE 6/71/243/244），排除本产品接口、软件 VPN、vEthernet、产品虚地址池及其他接口类型。蜂窝接口同样必须满足硬件标志、admin/oper/media 状态、有效 GUID/LUID、已分配且 preferred 的源地址及路由验证，不能仅凭类型获得许可。物理候选按最长前缀、总度量选择；同优先级无法由系统物理接口选择消歧时拒绝。最多读取3次快照，要求两次连续选择一致；活动 TUN 的更具体路由不会成为候选。PPP/软件桥仍不作为物理出口。原生 WWAN 两种类型的选择与拒绝路径已由 fixtures 验证；当前没有 WWAN 硬件，蜂窝拨号/传输/NLM 成本未做实机验收。

返回 `PhysicalRoute` 包含目标、接口 index/LUID、接口 `AdapterGUID`（不是 NLM 网络配置 GUID）、本地源地址、前缀、下一跳和总度量。`VerifyPhysicalRoute` 重新选择并比较完整快照；`DialPhysical(ctx, "tcp"或"udp", numericEndpoint, route)` 只接受相同目标，固定源 IP，使用明确 v4/v6 socket，在创建前、ControlContext 内和连接后分别验证。连接前设置 `IP_UNICAST_IF`/`IPV6_UNICAST_IF` 并回读，随后核实实际本地源地址。IPv4 设置值为网络字节序，读取值为主机字节序；IPv6 两者均为主机字节序，遵循 [Winsock IPPROTO_IP 文档](https://learn.microsoft.com/en-us/windows/win32/winsock/ipproto-ip-socket-options)。路由、源地址或接口发生变化就关闭连接并返回错误，不无条件重新选路继续同一请求。

这些 API 也供签名更新助手执行独立的物理路由及 NLM 成本核对。其返回只证明所检查时刻的快照；已连接套接字在完整传输期间的持续成本/路由检查由助手负责。真实 TUN/WFP 下是否按预期出物理口仍需抓包验收。

### 服务组合与固定控制面解析

`protectedTunnel.SetEntryDNS(entrydns.Policy) error` 校验并复制候选，仅影响下次 Prepare；不能修改当前会话。入口引导仅包含实际入口候选，完整 Arm 在 `AllowLAN=true` 时才加入可信 `NetworkSnapshot.DNS` 的本地解析端点，关闭 LAN 时不加入。WFP 规则是各项例外的并集：若同一端点同时被本地域名策略使用，完整 Arm 可为该独立用途授权双传输；LAN 一般业务规则仍保留设计规定的非53流量。

`protectedTunnel.ResolveControlHost(ctx, host) ([]netip.Addr, error)` 只接受签名服务编译变量 `ControlAPIHost`，默认 `client-api.sunip.cc`；要求已有保护意图，返回公共 IPv4 地址。它使用物理 `EntryDNS` 的自动策略，与用户入口自定义 DNS 分开。服务调用 `UpdateControlDNSForOwner`，在同一管理器锁内核实 SID 并原子替换该组服务专用 DNS 规则，再解析；不改保护意图、owner/account/resource、入口、LAN、TUN LUID、UDP relay 或 armed/recovery 标记。失败不提交新的内存策略、不继续查询、不放开 `svchost`。恢复状态可恢复控制面解析，但不会恢复业务证明。未激活时，owner 入口不安装规则，供固定更新助手执行正常显式解析；不能因并发解除/重新 Arm 绕过 owner 检查。`UpdateControlDNS` 保留为可信服务内部的无 owner 参数底层入口。

GUI/nativehost 的 IPC 命令和 HTTP 数字地址拨号由对应模块接线；必须保留原 URI/TLS 主机名与证书验证，不能把本接口变为任意主机 DNS 代理。本轮验证了服务接口与 WFP 事务组合，未证明已安装 GUI→服务→TLS 的实际链路可用。

更新助手由独立角色 `serviceipc.AuthenticateUpdateHelper` 验证：实际 pipe PID、固定安装路径 `BlueGrass.Update.exe`、可信发布者，以及同 SID/session 的签名 `BlueGrass.exe` 父进程。不能使用 `BlueGrass.Native.exe` 取得此角色，反向也不能取得 Native broker 角色。服务先核实固定助手文件的签名，才调用无路径参数的 `AllowUpdateHelper()`；它只按 GUI 所在安装目录派生助手路径，并授予该 app 出站 TCP443，虚拟池阻断优先。主机名白名单由签名助手和服务共享的 `cmdupdate.TrustedHosts()` 执行，WFP 不识别 TLS 主机名。

助手在既有 `BlueGrass.Control` pipe 上发送一次 `{"id":"...","command":"ResolveUpdateHost","host":"..."}`，无需 Hello；返回 `{"id":"...","ok":true,"addresses":["公共IPv4"]}` 或错误。该 worker 不创建 Native Host、不取得或释放会话租约、不执行连接/解除命令。空或无效的编译更新配置返回空白名单；任意主机、额外配置字段与变更会话命令均被拒绝。保护激活时仍核实相同 OS owner，DNS 使用服务自身的物理端点例外，助手只按返回数字地址连接443并保留 TLS 主机名。配置、实际签名安装和网络抓包验收仍由发布流程完成。

### 未确认清理、回调与 FakeIP 缓存

`protectedTunnel.block` 先撤销会话 token，提交 `BeginMaintenance`，执行 WFP Block，再 Close 原业务/本地 TUN；只有 Close 返回成功才清空相应引用。只在所有步骤成功后提交 revision 绑定的清理确认。`Prepare`/`Release` 必须先重试这些原对象并成功。服务级 `clientLifecycle` 在释放 pipe 租约前保存失败的 `host.Close`；后续客户端只有原 Close 成功后才能接入，否则 `RECOVERY_REQUIRED`。当前引擎的粘性清理错误不能由普通 Release/新客户端抹掉。重启无原对象时，由持久 `cleanupRequired` 触发 `tunengine.RepairAdapter` 精确归属清理；nil Go引用不等于清理证据。维护日志写入失败仍尝试阻断、关闭已有对象，但不会确认清理或在无对象情况下越过日志直接修复。

### 暂停期间独立本地 DNS

服务通过已有 `SetBusinessGate(false)` / `Stop` 接口保持 local-only，无需增加IPC命令。先撤销业务许可、关闭业务运行时并确认日志，然后从新物理快照提取可表示的本地解析器，`ArmLocalDNS` → `BeginLocalRuntime` → `NewLocalOnly` → `Start` → 校验网络generation → `AllowLocalTunnel`。本地运行时只使用快照本地区域规则；没有所选中转、公共DNS或FakeIP解析器。接口仍使用同一确定性 BlueGrass适配器，因此业务与本地运行时不能并存或越过清理确认替换。

local-only 许可只有精确活跃 LUID 的 TCP/UDP远端53，以及 LAN开启时的限定本地范围/非53规则。物理解析器只授予服务TCP53，普通应用没有物理53例外；虚地址继续优先阻断非DNS业务。link-local/带zone DNS不能由当前WFP契约准确表示，服务过滤后同时传给运行时和WFP，不自行去掉scope放行。LAN关闭时运行时仅保留localhost/拒绝行为，不授权物理本地解析。

local-only 的寿命不依赖已取消的连接尝试；入口DNS失败也会在清理确认后恢复此运行时。服务每秒检查暂停网络；变化、不可读或运行时自行退休时，先 Block撤销旧本地LUID、解析器及LAN快照，再关闭/确认，然后使用新快照重建。每次快照有取消期限，Start总预算10秒；失败保守阻断并由后续检查重试。旧取消的监视任务在锁内再次检查上下文，不能触及替代业务会话。显式 Release 和服务/host Close 停止监视并关闭本地运行时；启动恢复不会自动恢复它。

**控制面路由限制：** local-only 未注册GUI/更新助手公网443的服务direct转发，WFP也没有服务任意443例外。更新助手已独立绑定物理接口；GUI原始HTTP socket也需要保留TLS主机名并绑定已验证物理接口。`ResolveControlHost` 只解决服务DNS路径和数字地址，不能单独使GUI socket绕过TUN路由。GUI侧绑定属于主集成，未由本包证明完成；不得为绕过此问题添加服务全公网443许可。

relay 回调只获取独立 `relayMu`，不获取 `protectedTunnel.mu`。即使 TUN Enable/Close 同步等待回调，也不会形成该锁循环。Enable 过程中 `AllowTunnel` 尚未成功的回调立即被拒绝；已在途的授权必须先于 Block/新 Arm 完成；旧会话、取消会话和已关闭对象的迟到回调都不能到达 Manager。

`protectedTunnel.Diagnose(ctx, diagnostics.Binding)` 是可选异步诊断封装。只向捕获的当前、已允许 TUN 转发原 binding，最多30秒，并关联服务会话的取消；探测期间不持有 `mu`。返回时再次检查同一 TUN、会话 token、取消状态及报告 generation，旧结果不影响替代会话。仅可信引擎报告 `proxy_exit/failed/exit_mismatch` 时调用 Block → Close，保留保护意图和 FakeIP 缓存；Close 失败仍保留原对象。普通超时/诊断失败不解除保护。真实 Local/Tun 方法和 nativehost 的完整上下文认证、任务管理由对应模块接线；本层不接受诊断端点、不会启用未配置 UDP 探针。

`CachePath` 固定来自 KnownFolder ProgramData 的 `BlueGrass/fakeip.db`，不接受 IPC 路径。winprotect 创建并固定受保护父目录，tunengine 打开数据库时检查 ACL/重解析点等。暂停、EOF、服务关闭均保留缓存；显式 Release 依次执行 Block → 确认运行时 Close → `tunengine.ClearCache(path)` → WFP Release。缓存清理失败阻止解除保护；未确认 TUN 清理时根本不调用缓存清理。

独立维护入口 `winservice.ClearOwnedCache() error` 使用同一固定 KnownFolder 路径：`Lstat` 确认不存在时返回 nil，其余读取错误原样返回；存在时调用 `tunengine.ClearCache` 执行严格 ACL、路径与独占锁校验。它不创建目录、不递归删除、不忽略错误。`Uninstall` 在服务存在/不存在的两条路径均依次执行 `RepairAdapter` → `ClearOwnedCache` → `winprotect.Repair`，前一步失败立即停止；服务存在时先确认停止，全部清理成功后才删除服务。独立 Repair 命令由主集成在 WFP 解除之前调用同一函数。

## 保护意图持久化

通过 Windows Known Folder API 定位 `%ProgramData%\BlueGrass`，独占 `winprotect.lock`，状态为 `state.json`。目录与文件只授予 SYSTEM/Administrators，owner 必须为 SYSTEM 或 Administrators；DACL 受保护、不继承外部权限。检查并拒绝重解析点、硬链接、错误文件类型及普通用户预置的 owner；持有目录句柄时禁止目录删除/替换。

保存使用受同样 ACL 保护的随机临时文件 → 写入 → `FlushFileBuffers`（`File.Sync`）→ 关闭 → `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`。解除保存版本化 inactive 记录而非直接删文件。实际断电持久性和文件系统行为仍需安装环境验收。

v2记录保留原八个字段，并增加 `resourceKey`、`logicalSessionId`、`phase`、`cleanupRequired`、`revision`。阶段为 blocked/running/local/maintenance；后3者必须有清理标记。兼容旧调用为 unbound，重启转 legacy；v1按原规范八字段严格解码后迁移。没有密码、令牌、节点凭据、端点、TUN LUID、出口证明或GUI恢复凭据。采用有大小上限的规范 JSON，拒绝未知/重复/缺失字段、截断数据和无效身份。状态文件不供用户直接编辑。

服务可选接口为 `ResumeLogicalSession(engine.ResourceKey) (string,error)` 与 `BindLogicalSession(engine.ResourceKey,string) error`，SID由已鉴权服务owner派生，不来自IPC。Resume只对完全相同SID+资源键返回原ID；空字符串+nil表示没有历史意图。返回ID不证明资格或允许转发。Bind先落盘，匹配已有绑定可幂等，不能覆盖；Prepare再次要求已绑定完整键。每个业务/本地运行时构造前提交清理标记，清理前提交维护阶段；确认必须匹配当前revision。成功清理保留原逻辑ID，只有成功显式Release的inactive提交才清除。删除规则后写盘失败仍保留原ID和Active=true，重启重新阻断。

显式资源切换可选接口为 `protectedTunnel.SwitchLogicalSession(oldKey,newKey engine.ResourceKey,newID string) error`；Manager同名同签名。nativehost负责鉴权旧上下文及新profile，服务再用可信SID验证已有完整键和原ID。禁止跨环境/数据源/账户、相同资源键或复用原ID。服务停止暂停监视，关闭原业务/本地运行时并确认维护日志后调用Manager。Manager要求已阻断、无业务/本地LUID、无未确认清理、无bootstrap或当前runtime授权；先原子撤销旧入口/DNS/relay许可，再保存新的完整键和ID。失败保留旧绑定及阻断恢复状态；成功保留 `protectIntent`、SID和FakeIP缓存，必须对新资源重新Arm/运行时日志/资格与出口证明。切换不调用Release，不删除保护意图；普通Connect/Bind仍不能暗中切换资源。

`Repair()` 不需要业务登录或服务可用，但需要管理员权限和独占状态锁，因此须先停止服务/内核。仅负责自己的 WFP 对象与保护意图；独立修复程序还须组合 tunengine 的精确归属 TUN 清理及完整归属判定的系统代理恢复。此函数返回成功不等于完整网络修复工具全部验收通过。状态目录损坏、被不可信对象占用或无法写入时报告错误，不绕过 ACL 检查自动操作其他路径。

## 已执行的无防火墙写入验证

工作目录 `E:\workspace\APP\core`；Go 使用 `D:\Program Files\Go\bin\go.exe`。单元测试只执行纯策略/生命周期、注入的存储与防火墙替身、内存中 ABI/安全描述符检查；不执行真实 `New`、`Repair` 或 `diskStore`。替身用于隔离系统副作用，生产状态机和策略编译器仍是真实实现。

| 命令/检查 | 本次结果 |
| --- | --- |
| `go test -count=1 ./winprotect` | 通过；包含故障注入、重启归属、残留恢复和策略检查 |
| `go test -race -count=1 ./winprotect` | 通过：`ok bluegrass/native/core/winprotect 1.132s`，包含 bootstrap/UDP relay 回归 |
| `go vet ./winprotect` | 通过，退出码0 |
| `go build ./winprotect` | Windows amd64 编译通过，退出码0 |
| `go test -count=1 ./winservice ./winprotect ./serviceipc` | 本轮组合、DNS策略、只读助手角色测试通过，均替换系统变更边界 |
| `go test -race -count=1 ./winservice ./winprotect ./serviceipc` | 本轮通过，包含跨客户端清理与并发迟到回调 |
| `go vet ./winservice ./winprotect ./serviceipc` | 本轮通过，退出码0 |
| `go build ./winservice ./winprotect ./serviceipc` | 本轮 Windows amd64 编译通过，退出码0 |
| `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./winservice ./winprotect ./serviceipc` | 本轮 ARM64 交叉编译通过，退出码0；未运行 ARM64 二进制 |
| `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go test -c ... ./winprotect` | ARM64 测试二进制编译通过；未执行 |
| `cl.exe /c ... winprotect/testdata/abi_check.cpp` | 对 Windows SDK 10.0.26100.0 的大小、偏移、标志静态断言通过；只生成 `.obj` |

2026-10-12 物理绑定修复检查点（均使用上述 Go 路径）：

| 命令 | 本次结果 |
| --- | --- |
| `go test -count=1 ./entrydns ./winservice ./winprotect ./serviceipc` | 通过；无真实防火墙/路由/服务变更 |
| `go test -race -count=1 ./entrydns ./winservice ./winprotect ./serviceipc` | 通过，分别1.078s / 1.105s / 1.124s / 1.088s |
| `go vet ./entrydns ./winservice ./winprotect ./serviceipc` | 退出码0 |
| `go build -o "$env:TEMP\BlueGrass.Service-physical-checkpoint-20261012.exe" ./cmd/bluegrass-service` | 退出码0；临时未签名编译产物，不是发布包 |
| `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./entrydns ./winservice ./winprotect ./serviceipc` | 退出码0；未运行 ARM64 产物 |
| `$env:BLUEGRASS_PHYSICAL_SMOKE='1'; go test -count=1 -run TestPhysicalRouteWindowsReadOnlySmoke -v ./entrydns` | 通过；读取实际物理接口、路由并验证 IPv4/IPv6 TCP/UDP 未连接套接字的接口设置及回读，没有 DNS 查询、connect、发送测试包或修改配置 |

新增纯测试覆盖物理接口排除、TUN 更具体路由、最长前缀/度量/平局与不一致快照、路由/接口/GUID/源地址变化、ControlContext 在绑定前的精确端点及 family 校验、取消/绑定失败、错误源地址连接关闭；DNS UDP/TCP 用内存连接真实编解码，验证候选顺序、5秒总预算、关闭连接及绑定失败后绝不启用普通拨号。只读 smoke 曾实际发现 IPv4 socket option 读写字节序差异，修正后加入回归测试并重跑通过。

同日追加 WWAN 243/244 和异步诊断封装后，四包单元测试再次通过；race 分别为 winservice 1.119s、entrydns 1.068s、winprotect 1.119s、serviceipc 1.084s，vet、Windows 服务临时 EXE 和 ARM64 包编译均退出0。诊断测试覆盖探测前证明要求、原 binding 转发、Stop 及时取消且不等待持锁探测、旧结果不影响新会话、错误 generation/取消结果丢弃，以及可靠 mismatch 时 WFP 保留/失败清理引用保留。服务对真实 `*tunengine.Tun` 的 Diagnose 方法有编译断言。只读 IPv4/IPv6 接口 smoke 重跑通过，没有 WWAN 实机验证。

这次追加验证发生在共享引擎扩展协议依赖期间；普通主模块测试曾因缺少新增协议 go.sum 项停在 setup 阶段。为不改共享模块文件，上述最终四包验证显式使用引擎负责人提供的临时模块，并开启只读模式：

```powershell
$validationModule = 'C:\Users\33075\AppData\Local\Temp\bluegrass-protocol-validation-20261012-euclid\validation.mod'
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly "-modfile=$validationModule" -count=1 ./entrydns ./winservice ./winprotect ./serviceipc
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly "-modfile=$validationModule" -race -count=1 ./winservice ./entrydns ./winprotect ./serviceipc
& 'D:\Program Files\Go\bin\go.exe' vet -mod=readonly "-modfile=$validationModule" ./winservice ./entrydns ./winprotect ./serviceipc
& 'D:\Program Files\Go\bin\go.exe' build -mod=readonly "-modfile=$validationModule" -o "$env:TEMP\BlueGrass.Service-physical-checkpoint-20261012.exe" ./cmd/bluegrass-service
```

ARM64 构建使用同一 `-mod=readonly -modfile` 参数和上文的交叉编译环境。主线合并依赖后仍须按正常模块执行统一构建；临时验证并不替代该步骤。本轮未修改 go.mod/go.sum、nativehost/control、tunengine 或任何运行中的服务/防火墙配置。

race 验证所用环境：

```powershell
$env:PATH = 'C:\msys64\ucrt64\bin;' +
  [Environment]::GetEnvironmentVariable('Path','Machine') + ';' +
  [Environment]::GetEnvironmentVariable('Path','User')
$env:CGO_ENABLED = '1'
& 'D:\Program Files\Go\bin\go.exe' test -race -count=1 ./winprotect
```

ARM64 仅编译和 SDK 静态核对的完整命令（在独立 PowerShell 中执行，以免交叉编译环境影响本机测试）：

```powershell
$env:GOOS = 'windows'
$env:GOARCH = 'arm64'
$env:CGO_ENABLED = '0'
& 'D:\Program Files\Go\bin\go.exe' test -c `
  -o "$env:TEMP\bluegrass-winprotect-arm64.test.exe" ./winprotect

& 'C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Tools\MSVC\14.44.35207\bin\Hostx64\x64\cl.exe' `
  /nologo /c /EHsc `
  /I 'C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Tools\MSVC\14.44.35207\include' `
  /I 'C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\shared' `
  /I 'C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\um' `
  /I 'C:\Program Files (x86)\Windows Kits\10\Include\10.0.26100.0\ucrt' `
  "/Fo$env:TEMP\bluegrass-winprotect-abi.obj" winprotect/testdata/abi_check.cpp
```

覆盖的关键失败包括：保存意图失败时不激活、事务 begin/body/commit/abort 错误、激活失败仍保留意图、撤销失败使会话失效、删除成功但清除意图失败仍 `Active=true`、重启时保存恢复记录失败仍撤销 TUN 例外、未知归属/损坏状态不自动解除、其他 SID 无法认领、关闭句柄不解除、禁止直接切换已验证 LUID、所有固定地址池在重启重设后仍不通过控制/LAN 例外。

新增 bootstrap/relay 测试覆盖解析前保存意图再安装 WFP、bootstrap 不放行入口/TUN/relay、相同绑定直接补全完整 `Arm`、错误绑定与 scoped DNS 拒绝、bootstrap/完整 Arm 失败保持恢复、relay 在出口证明前被拒绝、TUN 消失时拒绝、IPv4入口地址相等与服务进程/UDP/端口限制、64个去重上限、事务失败无新许可，以及 Block/重启/Release/重新 Arm 不恢复旧 relay。原生编码测试确认 relay 的非持久标志、IPv4出站层和四项精确条件；未调用真实 WFP。

服务组合测试直接运行生产 `protectedTunnel`、服务租约与 `nativehost.Close`，只替换 WFP/TUN/缓存写入/解析边界；覆盖构建失败后的残留、粘性 Close 失败跨客户端保留、Release 清缓存的顺序与失败、入口解析前安装阻断、自定义策略复制/下一次生效/无系统回退、字面量无自动 DNS、独立控制 DNS 更新不改会话、旧代次/取消/Enable/Close 中的回调和在途授权串行化。只读更新协议测试使用 `net.Pipe`，未调用真实服务、进程鉴权、签名校验或防火墙变更。

### v2 日志、本地 DNS 与显式切换最终所属范围检查点

本段更新前文较早的临时模块检查点：协议依赖已在共享正式模块中，以下命令使用正常 go.mod/go.sum、`-mod=readonly`，没有临时modfile。此线程没有修改依赖文件。

| 命令 | 最新结果 |
| --- | --- |
| `go test -mod=readonly -tags with_gvisor -count=1 ./winprotect ./winservice` | 新增显式切换后通过：0.070s / 0.064s |
| `go test -mod=readonly -tags with_gvisor -race -count=1 ./...` | 所属及其余包通过；winprotect 1.176s，winservice 1.109s，entrydns 1.068s，serviceipc 1.095s，tunengine 3.487s；该全量调用退出1，唯一剩余为并行开发中 nativehost 的 `TestSwitchResourceExpiryAfterJournalCommitRemainsRecoverable`（旧context/保护断言）。该检查点不宣称全量测试通过，主集成须在nativehost完成后复验。 |
| `go vet -mod=readonly -tags with_gvisor ./entrydns ./winprotect ./winservice ./serviceipc` | 退出0 |
| `go build -mod=readonly -tags with_gvisor -o "$env:TEMP\BlueGrass.Service-journal-local-checkpoint-20261012.exe" ./cmd/bluegrass-service` | 退出0；临时未签名EXE |
| `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -mod=readonly -tags with_gvisor ./entrydns ./winprotect ./winservice ./serviceipc` | 退出0；仅编译 |
| `BLUEGRASS_PHYSICAL_SMOKE=1 go test -mod=readonly -count=1 -run TestPhysicalRouteWindowsReadOnlySmoke -v ./entrydns` | 再次通过：实际Ethernet index11；验证v4/v6 TCP/UDP未连接socket的绑定/回读；无DNS查询、connect、发包或配置写入；无WWAN硬件验收 |
| `gofmt -l entrydns winprotect winservice` | 无输出 |

新增RED/GREEN测试先分别观测到缺少独立local生命周期、暂停没有local运行时、Block保留旧LAN快照、缺少显式切换接口，然后实现并回归。v2测试覆盖各持久边界的严格codec及重启回放、v1迁移、完整owner/key/ID、构造前日志、维护/确认写入失败、过期清理回执、Release tombstone失败。local测试覆盖独立接口53规则不开放业务、只有服务TCP53、旧物理LAN/DNS许可撤销、失败构造/Start/Close/WFP/日志、取消隔离、网络刷新与显式解除顺序。切换测试覆盖精确清理前禁止切换、同归属内新key+ID、旧入口/证明撤销、失败写盘保留旧binding及重启、其他SID/账户/环境/数据源拒绝、local运行时清理失败不能越过。

全部系统变更边界使用替身；没有调用真实WFP New/Repair、创建或删除网卡、变更路由、安装服务或清除实际缓存。并行nativehost实现中还曾出现暂时缺少switch类型/方法及新测试RED；后续服务/ARM64编译已恢复通过，上述最后一轮唯一剩余测试已明确交接给该模块负责人。

## Beta1 外部验收清单（均未在本次执行）

应在可恢复的 Windows 10/11 提权测试机或虚拟机执行，保留独立抓包点、时间线、所用 OS/驱动/版本、WFP 对象导出和失败日志。真实规则可能切断网络，先确认不依赖被测网络的控制与恢复路径。

| 验收项 | 必须证明的结果 |
| --- | --- |
| 首次安装和规则原子替换 | 所有 layer/条件/标志被目标 OS 接受；注入 add/commit 错误后没有部分新策略，旧策略的残余权限明确可见 |
| 入口 DNS bootstrap | 第一条入口解析请求前保护已提交；仅服务对所选 DNS 发包；解析失败保持阻断；完整 Arm 前没有 TUN/relay 业务许可 |
| 进程终止、服务崩溃、TUN 消失 | 业务 TCP/UDP、IPv4/IPv6、现有连接和新连接在物理出口均不泄漏；核实内核转发门和接口生命周期 |
| 重启、关机、升级、BFE/服务延迟 | 从最早可观察网络阶段到运行过滤接管没有禁止流量；boot-time/persistent 交接无泄漏；异常仍有可信恢复路径 |
| TUN 放行与撤销 | 仅当前精确接口获得例外；出口验证失败、过期、撤销、换资源、旧异步回调和 LUID 复用不放行业务 |
| v2日志/切换崩溃边界 | 完整owner/resource/logicalID保持；维护前后断电/升级/写盘失败不抹掉cleanupRequired；SwitchResource保留阻断，旧proof无效，新资源须新证明 |
| 暂停local-only DNS | 业务清理确认后才启动；.lan/home.arpa/受控mDNS在暂停与入口失败期间可用；公网名称拒绝；LAN关闭无物理解析；网络变化先撤销旧许可；显式Release关闭local；重启仅阻断 |
| SOCKS UDP relay | TCP 初始 Probe 不提前开 UDP；认证响应仅允许明确返回的公网 IPv4 和精确端口，可与入口异址；普通进程及未授权端点不因 relay 得到许可；切换/异常后清理旧许可 |
| IPv4/IPv6、UDP 与存量连接 | ALE 重授权、已有 TCP/UDP flow、IPv6、接口重选和路由变化符合设计；不能只测试新建 TCP |
| LAN 开关、多网卡、网络切换 | LAN 使用正确本地接口；无路由不走中转；虚地址不出物理口；当前接口快照及时刷新；各 scope、mDNS、广播合法 |
| DNS | 普通应用向 LAN 路由器 TCP/UDP53 的公网查询不能直连；仅受控服务访问允许解析器；名称分类、绑定网络和缓存隔离由全链路验证 |
| 必要网络控制与 GUI | DHCP/续租、IPv6 ND/RA、GUI TCP443及控制面 DNS 在已声明例外内工作；缺少例外不能临时扩大到所有公网 |
| 存储 ACL 与故障 | 普通用户不能读写状态/锁或修改 ACL；预植目录、重解析、硬链接被拒绝；写入中断、磁盘满、重命名失败及断电后不自动失去保护意图 |
| 多用户和恢复 | 重启仍拒绝另一个 SID；原 OS SID 不登录业务即可显式解除；未知归属只由管理员修复；删除后写盘失败的 UI 仍显示待恢复 |
| 独立修复与卸载 | 服务/GUI 不可用时按自己的 GUID 和 TUN 归属恢复；不删他人 WFP 对象；代理仅按完整归属证据恢复；错误逐项报告 |
| 第三方安全软件/其他 VPN | WFP 子层仲裁、过滤器禁用、路由/接口变化和硬许可组合不会破坏保护；不以本包策略模拟器替代实际仲裁验证 |

禁止流量零泄漏计数必须明确是 TUN 模式，并扣除事先定义的控制面、回环、必要网络控制和已允许 LAN 流量；发现未知流量不能事后加入白名单。本包测试通过不关闭这些外部依赖，也不证明系统代理模式具有全局过滤能力。

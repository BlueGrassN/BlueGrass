# BlueGrass Android VPN 连接阶段

> 历史记录说明（2026-10-19）：本文保留原日期的方案、缺口和测试/构建事实；其中 sing-box 主内核、旧协议全量承诺及 libbox 路线已由 [ADR 0049](../../adr/0049-xray-only-four-platforms.md) 替代。四端仅使用 Xray-core，旧报告不能作为新核心或其他平台的验收证据。原固定预期出口门槛已撤销，短信为可选能力。

> **For agentic workers:** 使用既有设计执行；平台和 Flutter 按以下固定契约并行实现，主会话实现 Go 内核并集成验证。当前目录含依赖的未提交代码，继续原地工作，不改 Windows/API、不提交混合工作区。

**Goal:** Android 用户可授权 VPN、连接有效资源、断开、查看实际出口；前台服务拥有 TUN 与内核，后台继续运行。

**Architecture:** Kotlin VpnService 持有 TUN 原始 fd；Go 移动桥接内嵌 sing-box 1.14.2，dup fd 后拥有副本，复用 Windows 纯 Go protocolconfig/exitprobe。Flutter 只传递刚刷新的节点配置，不将账号 token 传给内核。

**Tech Stack:** 已安装 Flutter/Dart、Go 1.27.2、Android API 26/target36、Kotlin、NDK 28.2.13676358、锁定 gomobile 0.1.12。

**Spec:** 既有主设计/补充设计 Android 章节、ADR 0011/0012/0031/0032/0033，以及第一批 README 的下一阶段；用户本轮已明确开始。出口仅观测，失败不阻断连接。

## 固定桥接契约

MethodChannel `bluegrass/vpn`：`prepare -> bool`（系统授权 UI），`connect(Map) -> snapshot`（前台服务受理即返回 connecting），`disconnect -> snapshot`，`pause({reason}) -> snapshot`（停止内核保留 TUN），`status -> snapshot`，`observeExit -> snapshot`。EventChannel `bluegrass/vpn/events` 广播完整快照，订阅后先发送当前快照。

connect 参数：`requestId`（UUID）、`resourceKey`（Dart ResourceKey.stableId）、`accountKey`、`name`、`config`（JSON 字符串）。JSON：`version:1, host:string, port:int, protocolConfig:既有v1协议对象, expiresAtUtc:string|null, effectiveNowUtc:UTC string`。上限 32768 UTF-8 bytes。不得写日志/Intent extras/磁盘；Service Intent 只包含无秘密的动作，进程内请求仓库存配置。

snapshot：`version:1, runtimeId:UUID, generation:int, state:idle|connecting|connected|paused|error, requestId:string|null, resourceKey:string|null, accountKey:string|null, tunActive:bool, errorCode:string|null, observedExitIp:string|null, observationState:unknown|checking|available|failed`。generation 在请求/状态变化递增，同 runtime 旧值不能回滚；异账号不得接续。状态/错误不包含节点密码、token、入口或异常正文。

Go AAR 包 `com.bluegrass.core.bridge`，库 `bluegrass`：`Bridge.start(config: String, tunFd: Int, platform: Platform): Session`；`Session.close()` 幂等；`Session.observeExit(): String` 成功为 public IPv4，失败抛固定 EXIT_PROBE_FAILED；`Bridge.validate(config)` 校验无网络；`Bridge.version(): String`。

Go Platform（gomobile 大写映射小写 Java）：`protect(fd:Int)`（失败抛异常）；`resolve(host:String):String`（当前物理 Network 解析得到的 IPv4，用逗号分隔）；`networkInfo():String`，返回 `{name,index,mtu,addresses:[ip/prefix],dnsServers:[literal ip],domains:[local search domain]}`。始终取绑定的非 VPN 物理 Network。Go 负责 public IPv4 入口准入和协议参数校验。

审阅后补齐：Platform 增加 `isValid():Boolean`，由 Android elapsedRealtime、当前请求和网络代次共同判定；Go 在现有 TCP/UDP 转发读写前及阻塞读取后校验，不仅依赖唤醒定时器。networkInfo 可带非默认 `routes:[prefix]`。

MethodChannel 增加 `updateResource({runtimeId,requestId,accountKey,resourceKey,config}) -> snapshot`：仅当前绑定会话接受来自成功资源刷新的新配置；传输参数变化暂停并保留 TUN，相同传输更新原生单调期限，覆盖从无限期到有限期、缩短及成功续费。失败刷新不更新或延长期限；按 ADR0015，临时 API 故障保留仍有效的已有会话，仅显示同步错误。

VPN：MTU 1500，`172.19.0.1/30` 与 `fdfe:dcba:9876::1/126`，DNS `172.19.0.2`，路由 `0.0.0.0/0` 与 `::/0`；建立前核对与物理地址/非默认路由不冲突。排除本 app 作为控制面；不 allowBypass，不整段排除 LAN。原生套接字 protect 后 Network.bindSocket。TUN DNS 劫持先于 LAN 分类，公网 DNS 经 selected，明确本地名称经物理 DNS/mDNS；公网 IPv6 拒绝。

## 生命周期约束

- prepare 每次检查授权；拒绝保持原连接状态，不谎报成功。通知前台服务、撤销/主动断开明确清理。
- 同一时刻一个 session，旧 connect/probe 结果不得覆盖新请求。切换/网络丢失/过期暂停时保留 TUN，禁止直连兜底；重连保留原期限，以 elapsedRealtime 执行。
- 配置仅内存，不提供系统常驻/重启自动连接；Manifest `SUPPORTS_ALWAYS_ON=false`，START_NOT_STICKY。进程被系统杀死仍存在系统层保护限制，不能宣称 Beta 断线零泄漏。
- 退出先停止 VPN，清理失败不完成退出；刷新发现活动资源到期/撤销时暂停。UI 重建读取原生快照，同账号接续，异账号停止后再登录。

## 工作包

1. **Go** `Android/native/core/`：先测试配置、DNS/IPv6/LAN 顺序、保护回调及错误脱敏；实现嵌入生命周期、平台接口、明确 outbound 出口探测。编译 ARM64 AAR，保留可复现脚本和许可证。
2. **Kotlin** `Android/android/`：实现 VPN 平台通道/事件、授权、前台通知、服务串行状态机、物理网络绑定、单调期限、暂停/切网；补可编译设备测试。只在 AAR 到位后 Gradle 集成编译。
3. **Flutter** `Android/lib/`、`test/`：先写拒权/过期/退出/迟到事件测试；连接前 getNode+prepareProfile，恢复原生状态；显示连接、保护和出口状态，禁多次重复连接。
4. **集成**：Go 测试/构建、flutter analyze/test、ARM64 APK、AAR 内容与 APK 元数据验证、独立审阅与回归修复。无设备时明确未做系统授权/抓包/UDP/切网真机验收，不自动使用真实账号。

## Review Focus

配置泄漏；旧请求迟到；未授权成功；TUN fd 双关/泄漏；公网 DNS/IPv6 直连；网络切换与过期延长；退出前原生停止；内核启动失败保留保护；设备测试未运行不能记为通过。

## 本地实施结果（2026-10-13）

四个工作包已完成代码、集成、独立审阅后的回归修复及本地构建。Flutter 94 项、JVM 状态机 27 项、Go race 215 个测试/子测试通过；Go vet 与 Flutter analyze 通过；Android lint 0 errors/3 warnings，设备测试 APK 编译通过。最终版本为 0.2.0+2 ARM64 debug，内嵌 sing-box 1.14.2，AAR JNI 与 APK 元数据/16 KiB 对齐已检查。

ADB 未连接设备，系统授权、真实 TUN/各协议转发、抓包、UDP、休眠和切网尚未实机验收；没有执行生产账号登录、数据库操作、部署或 Git 提交/推送。细节、SHA-256、审阅修复和待验收步骤见 `Android/docs/vpn-verification.md`。本阶段完成的是内部 Alpha 的本地实现与构建，不等于已满足 Beta/发行门禁。

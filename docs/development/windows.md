# Windows 开发手册

2026-11-06 共享架构：平台工程使用根 `packages/client`，公共业务包、Go/Xray 与契约分别位于 `packages/`、`core/`、`contracts/`；新公共功能只维护一份实现。迁移后的开发和验证要求见 [共享客户端架构](shared-client-architecture.md)。

2026-11-06 发布规则：所有可交付平台统一使用固定 [preview Release](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)，各端版本独立更新，不再自动创建平台 tag。构建/发布、来源核验和失败恢复见 [统一 GitHub 发布手册](github-releases.md)。 发布器自行筛选 main 成功运行，实际更新须核对 preview 逐端来源与附件，不能仅看发布任务成功。

本手册与[四端共享手册](four-platform-development.md)、[ADR 0049](../adr/0049-xray-only-four-platforms.md)共同执行。Windows 10/11 x64 的唯一代理内核为 **Xray-core v26.10.17（上游预发布）**，模块/提交/校验值以共享手册为准。禁止 sing-box 回退或第二代理内核。

## 工程与迁移状态

入口为 [Windows/README.md](../../Windows/README.md)。Flutter 工程位于 `Windows/app/`；共享业务包在 `packages/`；Go 宿主、服务及平台保护在 `core/`；契约与构建入口分别在 `contracts/`、`Windows/scripts/`。

Xray 迁移已落实到共享出站、本地 HTTP 网关和 Windows TUN 适配。原生 TUN 迁移记录见 [Windows Xray 原生 TUN 验证](../../Windows/docs/xray-native-tun-verification.md)，之前的协议内核迁移记录见 [Windows Xray 迁移验证](../../Windows/docs/xray-migration-verification.md)。历史系统代理/TUN/WFP 记录只证明各记录指定的代码与产物，不能转记为新内核通过。

## 数据面与保护要求

- Go 原生宿主/服务内嵌 Xray，保持结构化请求、可信 IPC、owner/attempt 与逻辑会话隔离。GUI 持有业务会话，服务不持有账号 token。
- 本地 HTTP 代理继续固定 `127.0.0.1:12808`。端口占用须明确失败，不随机换端口、不终止占用进程；重连和暂停恢复保持该端口契约。
- 首次默认系统代理，采用当前用户 WinINET 设置及归属事务；原生就绪后启用，出口观测随后进行。暂停时保留阻断的入口/设置，显式断开按归属恢复；外部修改不能被无条件覆盖。
- Windows 增加独立的“国内外分流 / 全局代理”和“TUN 模式”按钮。新安装默认分流，旧偏好缺少路由字段时保持全局。两种接管方式使用同一固定 geosite/geoip 分类；TUN 国内直连仅授予当前服务会话精确端点许可。DNS、规则更新和验收方法见[路由模式说明](../../Windows/docs/routing-modes.md)。
- TUN 使用受信任安装环境中的 Go 服务和平台适配。使用 Xray 原生网卡与 TCP/UDP 栈，必须保持 WFP 持久保护意图、调用方身份校验、受控物理出口和精确归属的修复清理。
- 保持入口 DNS 与公网业务 DNS 分离、LAN 规则及公网 IPv6 阻断要求。FakeIP/FakeDNS 是需重新验证的策略，不允许直接透传旧内核字段。
- 服务或内核异常不能导致全局模式业务直连兜底。系统代理模式只覆盖进入代理的流量，不能套用 TUN 的断线保护声明。

协议范围以[固定版本矩阵](xray-protocol-matrix.md)为准；TUIC/AnyTLS 显示不支持。TLS、认证、路由或保护失败不能换协议、换资源或切回旧内核。

Windows 原生 TUN 使用固定 Xray 的 `proxy/tun.NewTun` 和 `NewStack`，删除 sing-tun/gVisor 运行时及 `with_gvisor` 构建标签。WFP 继续由服务独占，物理接口按会话显式绑定。官方 Wintun DLL 与许可证随 `runtime/` 分发，构建时核对 ZIP、DLL 校验值和签名。sing 通用地址/缓冲工具及本地缓存不是网络内核。

Xray UDP 按源 socket 汇聚，需要保留逐包目的地址并分别执行策略；DNS 与普通 UDP 不可共用首包授权。旧、新版 BlueGrass 的两个确定性网卡 GUID 都需核验名称与驱动身份；断开确认路由/DNS 清除后才允许释放 WFP。

2026-10-20 的[管理员实机验收](../../Windows/docs/elevated-tun-acceptance.md)已通过真实 TUN HTTPS/UDP、系统 DNS、暂停恢复、工作进程崩溃阻断及独立修复。修正了管理员 WFP owner、DNS 误指向主机自身地址，以及误拒绝认证 SOCKS 返回的异址公网 UDP 中继。IPv6/LAN 无可用基线的项目、签名 SCM 安装、重启、休眠和多用户测试不计入本轮通过。

2026-10-21 的[路由模式验证](../../Windows/docs/routing-modes-verification.md)记录了分流与全局的系统代理/TUN 实测，以及最终全局连续三轮启停和异常恢复。+19 修正逐 socket 全网卡枚举、Windows DNS 异步清理确认与本地网络快照的过短时限；不放宽 WFP 端点范围或清理成功条件。新包自动替换固定解压目录，覆盖前发现旧客户端仍在运行时须先停止展开，保留旧目录完整。

Windows 原生 TUN 的 DNS 必须指向所属虚拟网段的未分配对端，不能指向 `Gateway` 中已分配给主机的地址。UDP 中继必须逐关联核对原始认证响应，允许不同于 TCP 入口的精确公网 IPv4；服务只为该 IP/端口添加当前会话许可，不能扩大为全部 UDP 或普通进程许可。

## 开发与验证入口

仓库根目录执行下列已有入口。它们是待执行命令，不表示本次文档更新运行过这些检查：

```powershell
./Windows/scripts/check.ps1
./Windows/scripts/check.ps1 -NativeIntegration
./Windows/scripts/build.ps1
./Windows/scripts/run.ps1
```

管理员实机测试使用 `test-elevated-tun.ps1 -PrepareOnly` 和随后 `-Run -RunDirectory ...` 两步，具体命令和恢复机制见上述验收文档。普通测试不启用真实 WFP/TUN，提权使用运行脚本的 PowerShell，不修改系统执行策略。

`-NativeIntegration` 涉及实际 Windows 原生环境；运行/真实连接按受控环境范围执行。Go 工具链应满足固定 Xray `go.mod` 的 Go 1.27 要求，具体补丁版本写入构建证据；不要沿用旧 sing-box 标签来推断功能已经编入。

产物目标仍为 `Windows/dist/BlueGrass-Windows-x64-dev.zip`，便携包根仅包含启动器和 `runtime/`。新包必须从实际二进制提取模块信息，核对 Xray 版本，检查没有旧内核载荷并更新第三方声明。ZIP 存在或开发包启动不等于安装了签名服务，也不等于可公开发行。

## 必须回传的结果

1. 当前共享 Go 配置/核心、Flutter 与跨语言契约检查结果；真实 Xray 受控 TCP 出站，TUIC/AnyTLS 拒绝，证书/认证失败，暂停恢复与统计归属。
2. WinINET 代理启用/恢复、12808 冲突、父进程退出、资源切换及外部代理设置变化。
3. TUN/WFP 提权实机 TCP/UDP/DNS/IPv6/LAN、切网、服务崩溃、重启、独立修复和多用户归属；抓包分别识别合法控制面与业务流量。
4. ZIP/安装包哈希与真实模块版本；签名安装卸载、升级回退和更新清单验证。

默认 API、官网、远程配置与跨域会话规则统一遵循[共享手册](four-platform-development.md)。旧报告入口保留在 Windows README，不把过去的通过数量或版本号当作新内核结果。

# 管理员 TUN / WFP 实机验收

本工具只用于 Windows 本机验收。它直接组合正式代码中的 Xray TUN、物理出口绑定、WFP 和持久化恢复组件；不安装 SCM 服务，不改变正式安装器、签名或 IPC 授权要求。普通 `go test` 会跳过所有需要管理员权限的测试。

## 运行

在已允许执行项目脚本的 64 位 PowerShell 中：

```powershell
$env:GOCACHE = 'E:/workspace/APP/Windows/.artifacts/go-cache-xray'
$prepared = ./Windows/scripts/test-elevated-tun.ps1 -PrepareOnly -MaxSeconds 240
./Windows/scripts/test-elevated-tun.ps1 -Run -RunDirectory $prepared.RunDirectory
```

准备阶段编译当前源码并复制官方 Wintun DLL，不提权、不修改网络。运行阶段通过 UAC 提权，先启动独立恢复进程，再开始实测。修改测试源码或脚本后必须重新准备，不能复用已经启动过的运行目录。

账号默认从仓库根目录 `testaccount.txt` 读取。账号、令牌、节点凭据和出口观测只保留在内存中。事件日志仅含阶段、资源序号、数量、布尔值和稳定错误码。业务探测使用不同于服务组件的独立 EXE，避免误享服务的 WFP 放行规则。

每次运行目录位于 `Windows/.artifacts/verification/elevated-tun/runs/`，包含：

- `manifest.json`：本次源码构建出的工具、驱动和脚本散列。
- `events.jsonl`：脱敏的实机阶段结果。
- `summary.json`：验收、崩溃检查、恢复和环境比较结果。
- `baseline.before.json`、`baseline.after.json`：系统代理、路由、DNS 的摘要，以及 10808 监听进程身份。
- `watchdog.status.json`：独立恢复进程的最终状态。

`launch.json` 只表示启动，不代表测试通过。必须检查 `summary.json`、恢复结果和测试前后比较。

## 测试范围

测试从生产 API 完整分页获取连接资源，选择能够实际通过 Xray 访问 HTTPS 的线路，不按固定出口筛选。随后验证真实 Wintun、HTTPS 出口、UDP STUN、UDP/TCP DNS、系统 DNS、双向统计、暂停阻断、恢复连接和正常断开。具备可用基线时，额外检查公网 IPv6 阻断和局域网 HTTP 开关。

最后只让本次验收工作进程异常退出，检查持久 WFP 仍阻断业务，再由独立进程清理本产品网卡、FakeIP 缓存及 WFP，确认直连网络和系统 DNS 恢复。

工具不结束或修改 10808 所属代理进程，不重启计算机、不执行休眠测试。机器级 WFP 阻断期间其他网络程序可能暂时无法联网。已有 BlueGrass 防护会话或同名网卡会阻止验收，避免覆盖现有状态。

管理员组件验收与正式签名安装、LocalSystem SCM 生命周期、多用户隔离、重启和休眠验收分别记录，不能相互替代。

## 2026-10-20 实测结果

运行 `4df986973b4f4303939055398c5f44b1` 于北京时间 14:47 完成，`summary.json` 为 `passed`。主工作进程按设计退出 92，随后崩溃检查和独立恢复进程均退出 0。独立 watchdog 已在工作进程启动前就绪并监护至完成，本轮恢复由 supervisor 调用的独立进程执行；没有故意终止 supervisor 来触发 watchdog 接管。

| 项目 | 实测结果 |
| --- | --- |
| 生产 API | 登录、完整分页获取 5 条连接资源、详情及退出登录成功；未按固定出口筛选 |
| 接管前阻断 | WFP 存在，独立业务进程收到 Windows `WSAEACCES` |
| Xray 原生 TUN | 实际创建 Wintun，并通过独立进程完成 HTTPS |
| 出口 | HTTPS 与 UDP STUN 观察到的出口均与所选代理一致 |
| DNS | 手动 UDP/TCP 查询及 Windows 系统解析器均获得 FakeIP；系统解析使用唯一子域，避免缓存干扰 |
| 统计 | 上传、下载计数均增长 |
| 暂停与恢复 | 暂停时业务收到 `WSAEACCES`，恢复连接后 HTTPS 成功 |
| 正常断开 | WFP 移除，物理 HTTPS 和系统 DNS 恢复 |
| 工作进程崩溃 | 崩溃前 HTTPS 成功；进程退出后 WFP 仍存在，独立业务进程收到 `WSAEACCES` |
| 独立修复 | 自有网卡、FakeIP 缓存、WFP 清理成功，HTTPS 和系统 DNS 恢复 |
| 环境恢复 | 路由、DNS、系统代理的前后摘要一致；10808 监听进程身份一致 |
| IPv6 / LAN | 本轮没有可用的公网 IPv6 TLS 或私网网关 HTTP 基线，未记为通过 |

本次实测发现并修复三处问题：

1. WFP 对象固定使用 SYSTEM owner 导致提升后的管理员遇到错误 1307。改为 Administrators owner，受保护 DACL 仍仅授权 SYSTEM/Administrators，不授予交互用户 SID。
2. Xray `Gateway` 会成为主机自身地址，不能同时作为 DNS 目的地址。DNS 改用同一 `/30` 内未分配的 `.2`，使 Windows 系统查询实际进入 Wintun；业务 TUN 和暂停时的本地 TUN 共用这一修正。
3. SOCKS 认证后的 `BND.ADDR` 可以与 TCP 入口不同。独立协议对照中，三条线路的异址公网 UDP 中继均实际响应 STUN，一条明确拒绝 UDP ASSOCIATE，一条认证失败。客户端现在接受认证响应中的精确公网 IPv4 中继；拒绝私网、CGNAT、虚拟池、域名、IPv6、未指定地址和零端口。每个关联仍需独立认证证据和当前会话授权；只放行服务进程的指定 UDP IP/端口，保留 64 活跃端点限制与引用计数回收。

反向回归已确认：旧 DNS 目的地址触发 `TestNativeDNSUsesUnassignedOnLinkPeer` 失败；旧“中继必须与入口同 IP”条件触发 `TestSOCKSRelayAllowsAuthenticatedPublicRelayOnDifferentHost` 失败。修正后相关 TUN/WFP/服务回归通过，未引入第二内核或绕过正式签名校验。

检测目标先在当前物理网络建立 HTTPS 基线，再固定字面 IPv4、SNI 和 URL 用于整轮比较。本轮采用 Cloudflare `/cdn-cgi/trace`；ipify 在本机存在重置或部分地址超时，未用这些不可用基线证明阻断。

完整 `go test -p 1 -race -count=1 ./...` 和 `go vet -p 2 ./...` 已退出 0，日志分别为 `Windows/.artifacts/verification/elevated-tun-race.log`、`elevated-tun-vet.log`。首次 race 回归发现 Windows TCP/UDP 保留区不同，测试夹具反复分配到另一个协议不可用的端口；仅修正本地夹具为随机候选端口逐一绑定两个协议确认，未更改系统保留端口或用户代理。失败日志另存为 `elevated-tun-race-first.log`。

## 更新产物

`build.ps1 -Version 0.1.0-beta.1 -Build 18` 已退出 0。启动器及内部 GUI 的版本资源均为 `0.1.0-beta.1+18`。旧的固定解压目录已安全删除，再从新 ZIP 解压；根目录仅包含 `BlueGrass.exe` 与 `runtime/`。

- 启动器：`Windows/dist/BlueGrass-Windows-x64/BlueGrass.exe`。
- ZIP：`Windows/dist/BlueGrass-Windows-x64-dev.zip`，56,764,570 字节，38 个文件。
- SHA256：`6c2d7d926752c547c3d837354a0a59fc8ae4864daacd86e8746f4ce2b239a99c`。
- `verify-release-bundles.py` 退出 0：ZIP、解压内容与当前原生二进制一致，Xray/驱动/图标/生产 API 正确，未包含验收程序或私密文件。
- `verify-portable-runtime.py` 退出 0：固定 12808、原生握手、状态和释放监听正常，系统代理未改变。
- 最终 12808 空闲，10808 仍由验收前相同进程 PID 49692 监听，没有遗留验收进程。
- 独立源码复核未发现明确的权限扩大或路由回归。系统为 Windows x64，内核版本 `10.0.26300`。

本次仍为未签名便携 ZIP，更新助手仍返回 `UPDATE_UNCONFIGURED`。管理员组件实测不等于签名 SCM 安装或正式自动更新验收；本次未修改 Android 或其他平台代码。

# Windows Xray 原生 TUN 验证

日期：2026-10-20。范围仅 Windows；Android 留给另一会话。交付版本 `0.1.0-beta.1+17`。

本文件保留 +17 的历史证据。之后完成的管理员真实 TUN/WFP 验收、三个实机问题修复和后续产物记录见[管理员验收](elevated-tun-acceptance.md)；文末“未进行真实 TUN”只描述 +17 当时的状态。

## 实现

Windows 系统代理和全局接管使用同一个固定版本的 Xray 代理实现。全局接管由 `github.com/xtls/xray-core/proxy/tun.NewTun` 创建 Wintun 网卡，`NewStack` 处理 TCP/UDP 数据包。没有 sing-box、sing-tun 或 gVisor 运行时，也不再需要 `with_gvisor` 标签。

Xray-core 仍锁定官方 v26.10.17（预发布），模块 `v1.260327.1-0.20261017152356-92ccc939aaf4`。没有修改上游源码或引入本地替换。sing 的通用地址/缓冲区工具和 bbolt 本地缓存仍是工具依赖，不执行代理协议或 TUN 协议栈。

服务保留 WFP 所有权、会话与资源门控、本地 DNS/局域网策略、FakeIP 映射、物理接口绑定及统计。Xray 设置自身虚拟网卡的地址、DNS 和 IPv4/IPv6 分割默认路由；不启用第二套全局 WFP/自动出口控制器。公网 IPv6 仍按产品策略阻断。出口 IP 在连接后观测，不恢复预期出口 IP 门槛。

SOCKS UDP 的认证、协商与数据报编码/转发现均进入共享 Xray 实例。客户端只在物理拨号边界核验原始认证和 BND 回复、申请/回收精确 WFP 许可。Xray 将 UDP 按源端口汇聚，客户端逐包保留目标地址，使 DNS、LAN 与普通业务分别执行策略。活跃目标和许可均有限额，关闭后可复用；不会累计到 64 个历史中继就永久失效。

固定 Xray 的 SOCKS UDP 编码缓冲区存在长度限制：IPv4 目标有效负载上限为 8182 字节，超限明确返回错误。旧版绕过该编码缓冲区的大包自定义收发实现已删除；不能把新版本描述为支持任意 65507 字节的 SOCKS UDP 业务包。

## 生命周期与驱动

- 网卡打开、栈创建、栈启动和网卡启动之间都检查取消/退休状态；失败回滚不能被报告为连接成功。
- UDP 双向活动延长空闲寿命，空闲源连接和目标流会回收；TCP 上传 EOF 后允许有界的下行响应；DNS 超时主动关闭连接，不依赖 Xray 中为空实现的 deadline。
- 旧版和新版 BlueGrass 的确定性 GUID 均受支持。重用前验证 GUID、精确别名、Wintun 存储名称和驱动身份，不能操作同名的无关网卡。
- 关闭后检查本产品路由、地址和 DNS 是否残留；Windows 自动生成的正常链路本地路由不会误报。所有权不明或清理失败时继续保留服务防护。
- 发行物附带官方签名的 Wintun 0.14.1 amd64。ZIP SHA256 为 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`；DLL SHA256 为 `e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce`。原始 WireGuard 签名与预编译分发许可证保留。
- 加载前核对 EXE 相邻 DLL 的散列和路径，校验到加载期间持有禁止写入/替换的文件句柄。不会从工作目录或 PATH 加载替代驱动。

## 验证记录

下列自动化检查已通过，日志位于 `Windows/.artifacts/verification/`：

| 检查 | 证据 |
| --- | --- |
| Go 全套 race，`go test -p 1 -race -count=1 ./...` | `xray-tun-native-race-final.log`，退出 0 |
| Go vet，`go vet -p 2 ./...` | `xray-tun-native-vet-final.log`，退出 0 |
| Flutter 361 项测试 | `xray-tun-flutter-tests.log` |
| Flutter analyze | `xray-tun-flutter-analyze.log`，无问题 |
| 跨语言契约 13 项 | `xray-tun-contracts.log` |
| API 登录请求不跟随重定向 | `test_live_api_transport.py`，1 项通过 |
| 真实 Xray 内存栈包级回归 | TCP、DNS→FakeIP、共享 Xray SOCKS UDP、多目标与 DNS/普通 UDP 混用、回包源地址、长时下行和空闲回收 |
| 权限与关闭回归 | 原始 BND/认证拒绝、并发关联隔离、控制连接 EOF、取消、单次许可回收、过期回调、64 活跃端点复用、半关闭和清理失败 |
| 实际 DLL 加载 | `xray-tun-loader.log`：从外部工作目录加载 EXE 相邻 DLL 成功；仅工作目录有 DLL 而 EXE 旁缺失时拒绝。没有创建网卡 |
| 安装器驱动与文件清单 | `xray-tun-test-build-installer.log`、`xray-tun-test-candidate-payload.log`、`xray-tun-test-wintun-validation.log`：真实签名 DLL、缺失/篡改驱动及清单、Inno 实际验证函数均通过；没有执行安装 |

回归期间曾发现测试服务器先分配 TCP 端口会连续落入 Windows 的 UDP 保留范围。仅调整测试夹具为先让 UDP 分配可用端口，再检查 TCP；没有修改系统保留端口或用户代理。

## 消融与简化

删除旧 TUN/gVisor 运行时、无用入站 JSON、旧栈接口适配及手写 SOCKS 数据报收发。使用 Go overlay 在不改动工作源码的情况下分别移除关键行为：

1. 移除 UDP 逐包目标选择：同源端口测试出现响应来源错误或超时，证明不能只沿用首包目标。
2. 移除平台冲突配置拒绝：全局策略测试发现不应调用的原生打开函数被执行，证明服务 WFP/物理出口所有权需要明确约束。

结果保存在 `Windows/.artifacts/ablation/xray-tun/results.json` 及对应日志。正常源码的完整回归通过。

## 产物与实机边界

已运行 `build.ps1 -Version 0.1.0-beta.1 -Build 17` 并退出 0。`dist/BlueGrass-Windows-x64/` 的旧解压内容已由打包脚本安全删除并替换，根目录只有 `BlueGrass.exe` 与 `runtime/`。两个主程序的 Windows 版本资源均为 `0.1.0-beta.1+17`。

- ZIP：`Windows/dist/BlueGrass-Windows-x64-dev.zip`（文件名保留既有命名），56,763,970 字节，38 个文件。
- SHA256：`235eb981b02443dc118f1dc5aa6af2ba3bd6beefd69cb085dc0594d19e31d27c`。
- `verify-release-bundles.py` 确认归档/解压文件逐字节一致，当前 Go 二进制与打包内容一致，新图标、生产 API 和许可齐全，排除私密/测试文件。实际二进制没有旧 TUN/代理栈依赖。
- `verify-portable-runtime.py` 确认固定端口 12808、Hello/状态、退出释放监听、诊断及更新助手行为。更新助手仍为 `UPDATE_UNCONFIGURED`，系统代理未改变。

使用 `testaccount.txt` 对生产 API 登录、完整分页、逐资源详情和退出登录复测。返回 8 条订阅摘要、0 条裸资产、5 条连接资源；所有连接资源均无预期出口字段，未按出口过滤。API 仍声明保守归属覆盖，不能声称分页代表账号全部资产。

5 条线路中，4 条通过包内 Xray/本地 HTTP 完成真实 HTTPS 出口观测，外部观测与客户端报告一致、下载统计增长、暂停阻断业务、断开和进程退出正常。另 1 条客户端连接失败；独立直连 SOCKS 对照确认上游认证拒绝。测试脚本按这些失败返回退出码 1，没有将线路失败伪装为全部通过。

脱敏实测日志为 `xray-tun-live.jsonl`，不保存账号、token、节点编号、地址或密码。最终 12808 已释放；用户 10808 服务仍是测试前的 PID 4848。

安装器在注册/启动服务前验证已安装 Wintun 的精确哈希、版本与官方发布者签名，候选包校验必须具备全部必需文件。上述安装器代码和验证夹具已更新；本次交付仍为便携 ZIP，没有生成或执行正式签名安装包。

本机执行器不是管理员。本次没有创建真实系统 TUN、修改主机路由/DNS/WFP、安装签名服务或验收重启/休眠/服务崩溃恢复；内存包级测试和 DLL 加载不能替代这些实机验证。便携包仍未签名，自动更新仍要求正式签名与更新配置。端口 `12808` 和用户 `10808` 服务的边界保持不变。

参考：[Xray TUN 官方说明](https://xtls.github.io/en/config/inbounds/tun.html)、[Wintun 官方分发](https://www.wintun.net/)、[迁移计划](xray-native-tun-plan.md)、[Windows 手册](../../docs/development/windows.md)。

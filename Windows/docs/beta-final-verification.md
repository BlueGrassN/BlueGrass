# Windows Beta 集成验收记录（2026-10-12）

> 这是切换生产 API 前的集成检查点。后续已迁移至 `client-api.sunip.cc`；当前生产接口、登录 IP 契约、构建和实测见[生产 API 验证](production-api-verification.md)。本页旧域名、build 1 哈希以及“资源接口尚未部署”描述保留为当时的历史事实。

本记录覆盖当前 `Windows/` 实现；早期开发包、截图及其他文档中的历史哈希不代表本次产物。所有修改保留本地，未提交、推送或部署服务端。短信登录按用户明确要求预留，不属于本次 Beta 的强制验收项。

## 已验证的当前实现

| 检查 | 结果与证据 |
| --- | --- |
| 领域规则 | 129 项通过，静态分析通过 |
| API 与会话 | 162 项通过，静态分析通过；移除旧的未绑定网卡的 DNS 替换分支 |
| Flutter / IPC / 界面 | 322 项通过，`flutter analyze` 无问题 |
| Go 原生代码 | 全包 `go test -tags with_gvisor -p 1 -race -count=1 ./...` 和 `go vet -tags with_gvisor ./...` 通过 |
| 共享契约 | 12 项通过，包含 45 个跨语言协议配置向量 |
| Windows 桌面 | Debug 构建成功；真实 DPAPI 原子写入/路径限制与桌面登录/连接/主题/登出两项集成通过 |
| 原生 HTTPS | C++ `/W4 /WX` 编译、HTTP 分帧、证书主机名、取消及路由测试通过；独立 bridge 编译通过，物理绑定的真实 HTTPS 健康读取 HTTP 200 |
| GUI HTTPS bridge | 单独的真实 Windows MethodChannel 健康读取通过；登录与资源查询亦已在该路径实测 |
| 消融 | 25 项不同的行为消融已执行；删除必要机制的变体成功编译并触发预期断言，恢复后通过 |

统一检查：`.artifacts/verification/beta-unified-final.log`。逐模块日志由 `scripts/check.ps1` 保存。原生 HTTP 独立验证入口为 `app/windows/runner/control_http_test.ps1`；桌面 bridge 测试为 `app/integration_test/control_http_test.dart`。

## 这次补齐的集成边界

- “全部资源”真实查询订阅与资产、独立分页、详情只读、账号代次隔离；接口只能保证部分覆盖时明确展示，不从分页缺席推断回收。默认可连接视图使用完整资格校验，保留最近成功顺序。
- 资源切换先关闭转发，保留 WFP/系统代理设置，再为新资源创建逻辑身份并重新验证出口。失败尝试也在 ACK 时同步当前资源身份；首次 TUN 验证失败确认暂停后可以保留保护重试。
- journal 已提交而后续 dispatch 拒绝时，原生返回明确、绑定旧 context 与目标资源的恢复标记。Dart 仅接受严格匹配且 gate 已知关闭的合法回复，保留同一通道重新 Connect；任意缺失 context 仍拒绝。
- 启动自动连接和更新后恢复先读取原生更新器重新验证的签名策略；过期签名清单可保留最低版本限制，但不能授权安装。未知策略不会绕过连接门禁，登录、保护监控和资源轮询仍可用；未配置更新源的普通首次启动可正常使用。
- GUI 的受保护 API 请求使用物理网卡源地址与接口绑定的 socket，保留原始 TLS 主机名并验证证书。请求凭据留在 GUI 进程；无系统 DNS、普通拨号、代理或证书忽略回退。当前编译期 API 固定为 `https://test-api.dos.im/api/client/v1`。
- TUN、独立停止态本地域名解析、v2 恢复 journal、类型化多协议、更新后台下载与费用检查、当前资源诊断、默认关闭的崩溃队列均已集成。相应单元测试不等于下面尚缺的真实安装与网络验收。

## 真实代理及受保护通信测试

使用根目录 `testaccount.txt`，凭据不进入命令行、日志、仓库或产物。用户指定的预期出口为 `63.161.180.219`，测试绑定限定环境、账号与资源 11134。

原生系统代理回归已验证：错误出口返回 CONN-005；正确出口允许转发；Windows 默认 HTTP 请求观察到指定出口；Pause 阻断且保留设置；同资源重试保留监听端口和逻辑会话、更新 attempt 并重新证明出口；Disconnect、EOF、崩溃重启恢复精确原设置；外部代理修改被保留。完成后清理和登出均成功。

证据：`.artifacts/verification/beta-live-system-proxy-final.log`。测试前后 `10808` 均为同一监听地址和 PID，没有停止或改变该代理进程。

GUI 实测新增 `-ControlHttpIp` 参数：在未安装 TUN 的这台机器上，明确传入当前 API 的解析 IP，用新 GUI 物理绑定传输执行真实登录、节点读取和登出；连接与系统代理验证仍走实际原生程序。它验证真实平台通道与 API 通信，不声称已经验证 WFP 生效时的全局路径。

首次连续多目标桌面运行中，登录/连接测试通过，第二目标在 Flutter 调试连接启动阶段失败；单独运行第二目标通过。脚本改为每个目标使用独立 Flutter 启动进程。首次结果保留为 `beta-live-gui-first-attempt.log`，不计入完整通过结果。

调整后完整脚本退出 0，两项目标均通过，见 `beta-live-gui-final.log`。最终主机只读检查见 `beta-final-host-state.json`：`10808` 的监听地址/PID 与测试前一致，没有残留 BlueGrass 进程。

## 构建与安装器载荷

普通 Release 与账号限定测试包分别构建，使用生产入口 `lib/main.dart`。安装器从普通 Release 的真实文件创建独立快照，校验 30 个载荷文件的哈希、当前原生程序、CRT/许可和嵌入的运行权限清单。它是 **UnsignedCandidate 编译证据，禁止安装，不是可发行安装包**。

首次载荷校验发现诊断程序没有嵌入运行权限资源；补齐 `asInvoker` 清单，并在每次原生构建时重新编译修复/更新/诊断的资源后，重新构建和校验均通过。没有跳过或放宽校验。当前通过的候选目录为：

`.artifacts/installers/0.1.0-beta.1-1-532815623df14c4d818de7490f51da64/`

其中 `verification.json` 保存实际载荷与修复程序哈希。安装器 SHA-256：
`be45a5d5c7f1e2ccd7ff0b69149dfeecd4aec6b31484e588e81c77b379a1678c`。

最终 ZIP 清单由 `scripts/verify-release-bundles.py` 校验，结果写入
`.artifacts/verification/beta-bundles-final.json`；检查必要运行库、五个原生程序与当前构建的哈希、限定测试绑定以及账号文件/会话/测试源码的排除。该脚本不读取账号凭据或解密会话。

| ZIP | 字节数 | SHA-256 |
| --- | ---: | --- |
| `dist/BlueGrass-Windows-x64-dev.zip` | 42850868 | `e2450975b62c02f6401677f8016a8274277d58b5f5e4326ea810c72dac0bdbde` |
| `dist/BlueGrass-Windows-x64-live-test.zip` | 42859237 | `5b01a1def09c365827f2dabf1d70fe80b8063d183412c1c30eb56a155cef5769` |

两者均为未签名包，版本 `0.1.0-beta.1` / build 1。测试包仅包含指定账号/资源的非凭据出口绑定，解压整个 ZIP 后运行 `BlueGrass.exe`。当前 `app/build/windows/x64/runner/Release/` 对应最后构建的测试绑定版；安装器的独立快照来自普通版本，未带测试绑定。

复现入口（仓库根目录）：

```powershell
./Windows/scripts/check.ps1 -NativeIntegration
./Windows/scripts/test-live.ps1 -ExpectedIp 63.161.180.219 -ControlHttpIp 82.152.166.237
./Windows/scripts/ablate-controller.ps1
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 1
./Windows/scripts/build.ps1 -LiveTest -Version 0.1.0-beta.1 -Build 1
python Windows/scripts/verify-release-bundles.py
```

`ControlHttpIp` 是本次验证时 API 的真实解析结果，只用于显式测试；将来重跑应先重新解析固定 API 域名，不把此 IP 写入产品配置。产品使用固定 API 域名；TUN 受保护路径由服务解析，并保留原始 TLS 主机名。

## 尚未满足的完整 Beta 发行门槛

1. **权威资源数据与服务端部署**：现有线上 API 没有权威固定出口字段。普通包继续拒绝缺少固定出口的资源；账号限定测试绑定不能冒充正式合同。新增资源端点尚未部署，部分归属/退款语义仍缺权威映射；32 个新增 MySQL 集成 fixture 未在隔离数据库运行。
2. **实际签名与发布配置**：当前两处证书存储均未发现代码签名证书；原生更新器发行清单、发布主机和公钥仍未配置。不能生成虚假发布者、将测试密钥加入信任或用未签名包冒充正式安装与更新验收。
3. **已安装服务和网络实机验收**：当前 token 未提权，`BlueGrassService` 未安装。尚缺签名提权 TUN/WFP、真实 TCP/UDP 协议矩阵、IPv6/LAN/DNS 抓包、网络切换、到期/撤销、GUI/服务崩溃、重启、升级/修复/卸载、多用户及 Windows 10/11 矩阵与性能测量。
4. **独立检测与接收服务**：当前出口验证使用明确标注的第三方 HTTPS 服务；正式出口/UDP 检测和崩溃接收服务尚缺发布配置。未配置 UDP 时展示“未测”，未配置崩溃接收端时不上传。

因此完整 Beta 验收仍未完成。可运行的未签名包与禁止安装的安装器编译候选分别标注用途；源码、单元测试和一次 TCP 联通都不能替代这些门槛。

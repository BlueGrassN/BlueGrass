# Windows Xray 与新版 Logo 迁移验证

日期：2026-10-19。范围：Windows；Android 由另一会话继续，本次不构建、不验收 Android。

## 实现

- 固定 Xray-core v26.10.17（上游预发布），Go 模块 `v1.260327.1-0.20261017152356-92ccc939aaf4`，提交 `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`。
- 系统代理和 TUN 共用 Xray 出站配置与会话实现。独立 sing-tun 只做 Windows TUN 数据包适配，不含旧 sing-box 出站或回退。
- 根目录新版 Logo 用于登录、导航、关于页、窗口/任务栏/托盘图标；原始素材未改动，浅深色字标见 [来源清单](../../docs/branding/manifest.json)。
- 固定 12808、生产 API .vip、官网入口、资源名称/客户备注、72 小时到期隐藏、先连接再观测出口的行为继续保留。
- TUIC、AnyTLS 保留显示，连接时明确报告 Xray 不支持；其余受支持协议使用严格类型参数和 TLS 校验。

## 已完成的界面检查

- Windows Flutter 361 项测试通过，`flutter analyze` 无问题；见 `.artifacts/verification/xray-flutter-tests-final.log` 与 `xray-flutter-analyze-final.log`。
- 共享 domain 164 项测试通过，包括支持范围和资源保留；见 `Windows/.artifacts-domain-xray.log`。
- 页面布局生成 48 张、工具栏 8 张受控截图；覆盖浅深色和多个窗口尺寸。截图使用合成数据，不作为生产连接证据。

## 原生边界与复核

原生测试包含真实本地 SOCKS、Shadowsocks 与 Hysteria 服务器及回显端点。最后复核发现的异步握手取消、共享 QUIC 生命周期、正常 EOF、半关闭响应与大 UDP 封装边界均已修复并复测：

- Xray 物理 TCP 握手受 flow 生命周期管理；共享 QUIC UDP socket 受 outbound/session 生命周期管理，关闭首条流不会断开其他流。
- CONNECT 接管后将流的生命周期与 HTTP request EOF 分离；上行半关闭保留下行，按 30 秒空闲超时清理，持续收到数据会续期。固定 Xray 的部分出站不传递物理 FIN，不宣称所有协议支持完整物理 TCP 半关闭。
- 正常下行 EOF 与取消/错误分开，关闭定界的 HTTP 响应不会被误报为 closed-pipe。
- UDP 依据协议/地址封装开销限制负载，超限或空包明确返回错误，避免成功写入却静默丢弃。
- HTTP Serve 尚未登记 listener 时也会显式关闭拥有的端口，立即退出后可重新占用 12808。

最终 `go test -p 1 -tags with_gvisor -race -count=1 ./...` 与 `go vet -p 2 -tags with_gvisor ./...` 均退出 0。对应日志为 `.artifacts/verification/xray-native-race-final.log`、`xray-native-vet-final.log`。受控 TUN 测试还包含内存 gVisor 的 DNS→FakeIP→Xray TCP、认证 SOCKS UDP 往返；这些测试不创建主机 TUN 或修改 WFP。

跨语言契约 13 项及 API 重定向保护 1 项通过。核心代码使用 Go 1.27，实际编译器和固定版本见 `Windows/toolchain.json`。

## 产物与运行检查

- 版本：`0.1.0-beta.1+16`，Flutter release 构建。
- [启动器](../dist/BlueGrass-Windows-x64/BlueGrass.exe)，真实运行程序和依赖位于相邻 `runtime/`。
- [ZIP](../dist/BlueGrass-Windows-x64-dev.zip)：60,969,409 字节，37 个文件，SHA256 `58371164f9a6b8d22c328c5d7ece9f0c7bdcefa1c39ee783493239de472ac3f7`。文件名沿用打包脚本，程序内部版本为上述 Beta。
- `build.ps1` 已先删除固定的旧解压目录，再解压新 ZIP。ZIP 与解压目录逐文件一致；根仅 `BlueGrass.exe` 和 `runtime/`，没有 `.lib`、测试配置或账号文件。
- 主程序和启动器内的全部 9 尺寸图标与新 ICO 字节一致；新浅深色字标已进入 Flutter 资源包。
- 从最终原生 EXE 的模块信息确认 Native/Service/Repair 含固定 Xray，所有助手均不含 sing-box。Update、Diagnostics 不承担代理内核，不要求含 Xray。
- `verify-portable-runtime.py` 验证固定 12808、EOF 后释放、诊断助手与更新助手响应，系统代理设置未变。现有 `10808` 服务未修改。
- 包保持未签名便携版边界，更新助手报告 `UPDATE_UNCONFIGURED`；本次没有完成签名安装或提权 TUN/WFP 实机网络验收，也没有构建/验收 Android、iOS 或 macOS。

## 生产账号与实际线路

通过 `testaccount.txt` 登录生产 `client-api.sunip.vip`，分页读取 8 项订阅归属摘要、0 项裸资产、5 条连接资源。服务器仍声明保守覆盖，不能据此宣称客户全部资产快照完整；客户端未按某个 IP 过滤。

5 条连接资源均没有 `expected_exit_ip`：其中 4 条通过新包本地 HTTP 网关完成 HTTPS 出口检测，原生观测与请求出口一致、业务计数增加，暂停阻断以及主动断开/进程退出通过。第 5 条无法通过上游 SOCKS5 认证；独立 Python SOCKS5 对照也返回认证拒绝，因此记录为上游/凭据异常，不修改节点数据或增加额外连接前置条件。

测试结束已退出测试会话并关闭测试子进程。日志仅保存脱敏结果：`.artifacts/verification/xray-live-resources.jsonl`、`xray-live-direct-comparison.jsonl`、`xray-upstream-diagnosis.json`，不保存账号密码、token、节点凭据或节点地址。

## 消融与简化

删除旧 sing-box 协议注册、重复的 TUN Xray 会话包装，统一复用共享的 `StartOutbound`。保持外部资源/IPC 的封闭结构，平台只能补充可信物理接口绑定。

通过 Go overlay 在隔离路径测试移除两项逻辑，不修改工作源码：恢复“Dial 返回时立即取消”会使异步握手失效；移除物理 socket 取消处理会使未完成的握手在关闭后继续存活。两项消融均复现预期失败，记录在 `.artifacts/ablation/xray/results.json`。因此保留这些必要生命周期处理。

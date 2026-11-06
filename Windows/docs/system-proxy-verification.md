# Windows 系统代理验证（2026-10-12）

> 历史快照：本文的测试数量、包哈希与“最终检查”对应早期系统代理实现。当前结果与发行限制见 [Beta 集成验收](beta-final-verification.md)。短信登录现已按用户要求暂缓。

本轮在 `feature/windows-client` 继续原有 Windows 开发。全部新增代码、脚本与报告位于
`Windows/`。测试账号由仓库根 `testaccount.txt` 读取；预期出口由用户指定为
`63.161.180.219`。登录、节点读取及退出 API 均直接连接，没有借用 10808。

## 真实网络证据

`scripts/system-proxy-check.py` 已退出 0，覆盖以下操作：

| 操作 | 实际结果 |
| --- | --- |
| API 登录及资源探测 | 三个资源中唯一匹配 11134；未匹配资源不绑定该出口 |
| 启动及第二个宿主 | 启动无残留时设置不变；第二个写入进程被每用户锁拒绝 |
| 故意使用错误预期出口 | 连接失败，业务门关闭，Windows 设置不变 |
| 正常连接 | WinINET 四字段查询确认启用自身随机回环端口 |
| Windows 默认 HTTPS 请求 | InternetOpen 的 PRECONFIG 模式，没有显式代理参数；出口为 63.161.180.219 |
| 暂停 | 保留代理设置，默认请求返回 HTTP 503，没有回退直连 |
| 主动断开 | Flags、Server、Bypass、AutoConfigURL 与初始快照逐字段一致 |
| 父进程管道 EOF | 宿主退出 0，原设置完整恢复 |
| 强制结束原生进程后重启 | 原设置在新宿主启动恢复；没有把被杀进程说成已执行清理 |
| 模拟第三方修改代理 | 停止转发且不覆盖修改；强制结束后仍可重启，显式断开清除过时归属并报告“保留外部设置” |
| 最后清理 | 测试注入被撤销，原设置逐字段一致，测试 API 会话已退出 |

原生日志：`.artifacts/verification/system-proxy-live.log`。
Python 测试注入属于测试本身；应用的断开操作没有把测试注入的外部值写回旧快照。

生产出口验证仍用 `https://api.ipify.org?format=json` 严格校验 JSON、TLS、公网 IPv4、
响应大小与预期出口。Windows 默认请求测试使用同服务的纯文本接口 `https://api.ipify.org`。
之前一次测试因 WinINET 返回纯文本而触发脚本 JSON 解析失败，清理仍成功；改用明确的纯文本
端点并严格解析 IPv4 后，全流程重新通过。没有放松生产验证规则。

测试期间只改变当前用户默认 WinINET 代理；不安装系统服务、不改 WinHTTP、路由、DNS、
防火墙或现有代理进程。10808 的 xray 监听者不受控制。初始基线为 ProxyEnable=0、
记忆的 ProxyServer=127.0.0.1:10808；精确恢复包含这个未启用的记忆值。

## 恢复与审阅修复

- 原生写入失败但 `prepared` 记录仍存在时，原值未变可清理意图，不误判外部覆盖。
- Flutter 启动独立核对原生恢复，账号、资源到期及节点接口失败不再阻挡恢复入口。
- 终止的 IPC 对象不能复用；先等待其清理，再用一个新宿主尝试恢复，失败可再次点击恢复。
- 暂停、到期、进程故障仍视为需要释放网络状态，主按钮和托盘保留断开能力。
- 身份失效清除账号，但不把正在等待的网络清理强行改为 idle；清理期间禁止新登录。
- 外部修改后，启动保留恢复入口。显式断开通过 Relinquish 清除过时归属，不写外部设置，
  界面区分“恢复原值”与“保留外部设置”。EOF 不擅自丢弃这种记录。
- 原生断开确认要求业务门已关闭、状态已知、系统代理未应用且不存在未完成恢复。

上述五类主要恢复缺陷来自独立只读审阅；已加入真实子进程回归和真实 Windows 归属丢失测试。
记录见 `system-proxy-progress.md`，失败复现见 `.artifacts/verification/proxy-review-*-red.log`。

## 消融

`scripts/ablate-system-proxy.ps1` 使用独立目录副本。三项变体均成功编译并触发行为断言失败：

1. 删除代理 Apply 失败后的关门操作：失败事务仍在转发。
2. 删除 EOF/Close 的代理恢复：清理没有尝试恢复，也隐藏恢复失败。
3. 删除持久化后的归属重查：覆盖期间到来的外部写入。

恢复源码后同一组回归通过，原始记录在 `.artifacts/ablation/system-proxy/`。
连同此前 14 项，累计完成 17 项安全机制消融。此前四项等价简化保持：UI 重复代次、
API 重复 IPv4 校验、Go attempt 重复上下文、无必要的 context 包装函数。
当前 `proxyEngine` 只连接业务门和设置事务，不另建容器、通用代理管理层或协议工厂。
实验不代表吞吐量、CPU、内存或零泄漏测量。

## 复现

```powershell
./Windows/scripts/check.ps1 -NativeIntegration
./Windows/scripts/build-native.ps1
python Windows/scripts/system-proxy-check.py --account-file testaccount.txt --expected-ip 63.161.180.219 --native-executable Windows/native/bin/BlueGrass.Native.exe
./Windows/scripts/test-live.ps1 -ExpectedIp 63.161.180.219
./Windows/scripts/ablate-system-proxy.ps1
./Windows/scripts/build.ps1
./Windows/scripts/build.ps1 -LiveTest
```

首次没有绑定文件时先执行 `live-socks-check.py` 并指定 `--binding Windows/.artifacts/live-binding.private.json`。
Go 临时可执行文件曾遇到 Windows 文件占用，GCC 外部链接也曾瞬态失败；随后串行 race
检查通过。消融脚本先编译到持久的实验目录再执行，编译失败不算消融成功。
没有关闭安全软件、跳过 race 或将失败改成成功。

## 适用范围

这是未签名的 Windows x64 开发版本。系统代理覆盖遵循 Windows 默认代理的程序；
显式直连、公共 UDP、IPv6、DNS 拦截、WFP/TUN、跨重启保护、多用户登录切换及 Windows 10
实机仍未验收。LAN 当前只实现 WinINET 的回环、RFC1918/链路本地和本地名称例外，
不是 D-31 全部地址/路由语义。没有宣称全应用零泄漏。

通用 API 仍缺权威固定出口字段；普通包继续拒绝无固定出口的连接。测试包仅针对给定账号、
环境和订阅使用用户授权的固定出口绑定。短信、完整资产提醒、签名更新和正式安装器仍有待完成。
强制结束原生进程后的恢复发生在下一次宿主启动，不是实时守护恢复。

## 最终检查及交付

| 检查 | 结果 |
| --- | --- |
| 领域规则 | 73 项通过，静态分析通过 |
| API/会话 | 70 项通过，静态分析通过 |
| Flutter/IPC | 最后一次 107 项通过，静态分析无问题 |
| 原生 Go | 所有包 `go test -p 1 -race -count=1 ./...` 与 `go vet ./...` 通过 |
| 契约 | 8 项通过 |
| Windows DPAPI/模拟界面 | 2 项实机集成通过 |
| Windows 真实账号及系统代理 | 1 项实机集成通过，含登录、连接、出口、流量、断开及登出 |
| Release | 普通包及测试绑定包均构建成功；默认不含 API 代理配置 |
| 包内容 | UI、原生 exe、Flutter/VC++ 运行库及许可存在；不含测试账号文件或会话密文 |
| 原生版本 | 两个 ZIP 内原生程序的哈希均等于本次 native/bin 构建 |
| 测试后宿主 | ProxyEnable=0、ProxyServer=127.0.0.1:10808，无待恢复记录、无遗留原生进程 |

完整检查日志为 `.artifacts/verification/system-proxy-final-check.log`；它包含当时 106 项 UI
测试和 DPAPI 集成。随后新增启动失败后的绑定初始化回归并修复，最终 107 项见
`proxy-final-app-test.log`，最终静态分析见 `proxy-final-analyze.log`。
真实桌面结果在 `system-proxy-live-windows.log`，构建及包核对在 `system-proxy-release-*.log`
与 `system-proxy-bundles.json`。最终设置快照为 `system-proxy-final-state.json`。

初次实机测试脚本未等待新增的原生启动流程，现已显式等待初始化和恢复确认；随后一次登录
仍失败，当时只取得 customer=null，未捕获具体 API 原因。独立 Dart 适配器真实登录/节点读取
及完整桌面重跑均通过。断言现会保留客户端的固定错误提示，未增加自动登录重试来掩盖失败。

| 产物 | 字节数 | SHA-256 |
| --- | ---: | --- |
| `dist/BlueGrass-Windows-x64-dev.zip` | 17090972 | `C1D18C9EED00CC6117B2A127BACEBE369D584EF470958BC1BA3ECC4B1B9B6BEB` |
| `dist/BlueGrass-Windows-x64-live-test.zip` | 17099491 | `18A3939FE69826B6790262AA2ECFA56F0DBD54FD6EC302D45D8B9C0042289369` |

测试包含账号/资源限定的非凭据出口绑定，普通包无此绑定。两包都必须整体解压后运行
`BlueGrass.exe`；当前 `app/build/windows/x64/runner/Release/` 对应最后构建的测试绑定版。
所有修改保持本地，未提交或推送。

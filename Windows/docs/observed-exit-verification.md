# Windows 连接后出口检测验证（2026-10-12）

本记录对应用户明确修正后的连接流程，替代旧报告中的固定预期出口、匹配门禁和测试绑定要求。旧测试和旧 ZIP 的哈希仅作历史证据，不代表本次版本。

## 当前行为

- 从 `https://client-api.sunip.cc/api/client/v1` 完整分页获取节点，使用所选节点的真实协议、入口、认证和有效期连接。
- 不要求 `expected_exit_ip`，不将入口 IP 或用户此前给定的 IP 当作连接条件；新 GUI 连接请求省略 `expectedExitIpv4`。
- 引擎准备完成后开放连接，再通过当前节点进行 HTTPS 出口检测，显示实际公网 IPv4。沿用当前 IPv4 产品范围。
- 出口未知显示“暂未获取”，检测失败不暂停或断开；成功重试更新首页。
- 切换、暂停、取消、账号更换和到期仍退役原尝试，旧探测或诊断不能更新新连接。
- 删除测试绑定读取、构建开关、联调预期 IP 参数及相关重复门槛。兼容旧 API/IPC 的可选字段不参与准入或匹配。

详细契约见 [连接流程](observed-exit-flow.md)，本次消融见 [观测消融](observed-exit-ablation.md)。

## 验证环境与范围

测试账号从仓库根目录 `testaccount.txt` 在进程内读取。日志仅保留允许公开的阶段、计数、资源 ID、实际出口和布尔结果；没有保存账号密码、令牌或节点密码。

实测前于 19:07（香港时间）读取本机状态：系统代理关闭，保存的服务器为 `127.0.0.1:10808`，旁路列表保持原值；10808 当时没有监听进程。没有启动、停止或修改用户的该代理。原始快照保存在 `.artifacts/verification/observed-exit-host-before.json`。

## 集成检查记录

首次统一检查通过 domain 162、data_source 171、contracts 13、API 重定向回归 20、Go race/vet 及 Flutter analyze。Flutter 测试中 330 通过，2 项子进程超时测试失败，原始结果保留在 `.artifacts/verification/observed-exit-full-check.log`。失败必须修复并重新验证后才计为通过。

独立源码复查发现诊断结果未回写控制器观测快照，导致成功重试仍显示旧 IP 或未知；本轮已修复并通过当前会话、不同观测及迟到结果回归。

最终回归：

| 检查 | 结果 |
| --- | --- |
| Domain / data_source / contracts | 162 / 171 / 13 通过 |
| API 重定向回归 | 20 通过 |
| Flutter 全量测试 | 332 通过，`observed-exit-app-final.log` |
| 静态分析 | Dart 两包、Flutter analyze、Go vet 通过 |
| Go race | 首轮全模块通过；最后变更涉及的 control/nativehost/exitprobe/localengine/tunengine/diagnostics/winservice 再次通过，`observed-exit-native-final.log` |
| 新消融 | 两项均为基线通过 → 指定行为失败 → 恢复通过，另有 5 项脚本测试；共享源码哈希与隔离副本一致 |

两项 Flutter 失败通过仅修改测试夹具解决：源码子进程的 JIT 启动握手使用正常测试预算，握手／连接完成后才启用故意不响应场景的一秒短超时。额外加入 1200 ms 启动延迟，修复前可稳定复现，修复后仍检出真实命令超时。生产超时没有修改。

## 原生与系统代理真实连接

两种真实测试都从 API 返回的完整列表逐个获取详情，没有指定测试资源 ID。4 个节点的 `expected_exit_ip` 均缺失／为 null，连接配置没有该字段。

| API 节点 ID | 本地代理 HTTPS 实际出口 | Windows 默认请求出口 | 原生报告出口 |
| --- | --- | --- | --- |
| 8418 | 154.64.146.181 | 154.64.146.181 | 154.64.146.181 |
| 8487 | 63.161.176.172 | 63.161.176.172 | 63.161.176.172 |
| 11134 | 63.161.180.219 | 63.161.180.219 | 63.161.180.219 |
| 13110 | 198.87.96.137 | 198.87.96.137 | 198.87.96.137 |

这些值是本次观测，不是配置或未来连接要求。不同请求的出口若发生变化不会因此判定连接失败。原生本地代理同时验证真实 TCP 流量、暂停阻断及正常退出。

每个节点的系统代理测试均通过：单写入者、不要求预期出口的连接、WinINET 默认网络请求、暂停阻断、原监听器／逻辑会话下重试、断开恢复、父进程 EOF 恢复、原生强退后重启恢复、外部代理改动保留及最终精确恢复。所有测试会话正常退出登录。日志：

- `.artifacts/verification/observed-exit-live-native.log`
- `.artifacts/verification/observed-exit-live-system-proxy.log`

原生日志中的 HTTP 503 出现在主动 Pause 后的阻断测试，符合预期。

## Windows 桌面真实界面

资源实测从生产接口分小页遍历：4 个节点／2 页、6 条订阅摘要／3 页、0 条资产／1 页，界面展示 4 个节点。随后由真实 AppController 和随包 NativeClient 顺序连接所有节点，并通过各自本地代理发送 HTTPS 请求、执行诊断、检查首页实际出口、断开恢复。

结果为 `connected=4`、`https_observed=4`、`display_observed=4`、`display_unknown=0`、`restored=4`；4 次显示均为上表相应的实际出口。测试分别统计 HTTPS 检测成功与有效显示，不把两个请求地址相等作为产品条件。每个节点均保留截图：`.artifacts/screenshots/observed-exit/live-connected-0.png` 至 `live-connected-3.png`。已查看截图，首页“系统代理已连接”和“实际出口 IP”布局正常。

日志：`.artifacts/verification/observed-exit-live-gui.log`。没有用 fixture 数据替代生产节点，没有加载测试出口绑定。

GUI C++ MethodChannel 的物理接口 HTTPS 健康检查也通过，使用本次解析的生产地址 `192.140.166.247`，保留原域名 TLS 验证。三个桌面目标各自启动和退出，没有复用失效的 Flutter launcher。

全部真实测试后于 19:27 再次读取本机状态，`proxySettingsUnchanged=true`、`listenerStateUnchanged=true`，无 BlueGrass 残留进程。系统代理仍关闭，保存的 `127.0.0.1:10808` 与旁路列表均未改变；10808 的无监听状态也与测试前相同。证据：`.artifacts/verification/observed-exit-host-after.json`。

## 发行范围

本次构建：`0.1.0-beta.1+3`，生产入口 Release，生成 `dist/BlueGrass-Windows-x64-dev.zip`。

| 产物属性 | 验证结果 |
| --- | --- |
| ZIP 大小 | 42,848,563 字节 |
| SHA-256 | `611abdc3cce019fc6201b038dda083c6f15f0cf8360547ddabb3350b4b9399b6` |
| 文件版本 | `0.1.0-beta.1+3` |
| Authenticode | `NotSigned` |
| 打包校验 | 32 项；五个原生程序哈希与当前构建一致；CRT、Flutter 数据及许可证齐全 |
| API 编译期地址 | GUI、Dart AOT、Service 中包含生产域名，无旧测试域名 |
| 私密／测试文件 | 无账号文件、DPAPI 文件、测试绑定或集成测试源代码 |

解压整个 ZIP 后运行 `BlueGrass.exe`。未压缩的完整目录为 `app/build/windows/x64/runner/Release/`。版本和打包证据分别为 `.artifacts/verification/observed-exit-release-version.json`、`observed-exit-bundle.json`；构建日志为 `observed-exit-release-build.log`。

同一真实载荷重新编译了安装器证据候选：`.artifacts/installers/0.1.0-beta.1-3-aa8fc23d389d47b380dfaf37d8fe6655/`。30 个载荷文件的哈希、原生来源及执行清单验证通过；安装器 SHA-256 为 `e6f284b4625aee892fffd022bb0e7377efb61727a458fa326a9cd93f2c59b0da`。它是禁止安装的未签名编译候选，没有执行安装或修复，不作为用户发行安装包。

短信登录按用户指示预留。代码签名、签名安装升级以及提权 TUN/WFP 的实机矩阵仍需真实验收；本次系统代理连接和观测验证不能替代这些发行证据。当前 API 的全部归属摘要仍以其实际 `snapshot_complete` 声明为准；这与节点能否直接连接无关。

## 重现命令

在仓库根目录按顺序执行（真实系统代理与桌面联调不能并行）：

```powershell
./Windows/scripts/check.ps1
./Windows/scripts/build-native.ps1
python ./Windows/scripts/live-socks-check.py --account-file ./testaccount.txt --native-executable ./Windows/native/bin/BlueGrass.Native.exe
python ./Windows/scripts/system-proxy-check.py --account-file ./testaccount.txt --native-executable ./Windows/native/bin/BlueGrass.Native.exe
# 此 IP 是本次 DNS 结果；重跑时先重新解析生产 API。
./Windows/scripts/test-live.ps1 -ControlHttpIp 192.140.166.247
./Windows/scripts/ablate-observed-exit.ps1
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 3
python ./Windows/scripts/verify-release-bundles.py
```

账号密码只通过账号文件传入；上述命令不需要预期出口参数或绑定文件。未修改或部署 API 服务，未执行 Git 提交或推送。

# 连接后观测出口：脚本与隔离消融

最终冻结源码已由主任务于 2026-10-12 19:23 重新执行：两项行为消融均完成基线通过、指定行为失败、恢复通过，5 项脚本测试通过，`sourceSnapshotCurrent=true` 且 `changedSourcePaths=[]`。证据位于 `.artifacts/ablation/observed-exit-20261012-192340-05bbe98d213f4feea3a37c9eab95578f/`。同一源码的真实系统代理四节点回归也已通过，见 [当前验证](observed-exit-verification.md)。以下阶段记录仅保留过程。

本次采用用户更正后的流程：使用节点配置建立连接，再经该节点观测实际公网 IPv4。
没有 expected-IP 配置要求，不按 expected-IP 选择节点，也不通过预设地址匹配决定是否连接。
探测器失败属于诊断结果；有效会话保持 connected。

## 系统代理验收脚本

`Windows/scripts/system-proxy-check.py` 对外接口保持为：

```python
native_check(executable, node, key)
# key = {environment, dataSource, accountKey, sourceId, kind}
```

主任务的 `live-socks-check.py` 负责遍历返回节点并调用此接口。本脚本保留传入的
`kind`，支持 subscription/asset 身份，不注入 `expectedExitIpv4`，已删除旧 wrong-exit
拒绝用例和 binding/expected 比较。

检查顺序：

1. 现场读取完整 WinINET 配置元组作为本次恢复基准；Hello 不改配置，第二个代理写入者被拒绝。
2. 使用节点协议、入口、认证和有效期连接，确认业务转发及系统代理应用成功，并核对资源身份。
3. 使用 `INTERNET_OPEN_TYPE_PRECONFIG` 的 WinINET HTTPS 请求读取公网 IPv4。请求不显式指定代理、不关闭 TLS 验证。
4. 在已连接后，最多等待 30 秒获取当前尝试的公网 IPv4 观测。单次状态 RPC 等待不超过剩余预算或 5 秒；期间只发送 GetStatus。
5. 每次观测轮询核对连接、gate、代理应用状态、监听地址和完整 context。迟到的其他尝试结果不能冒充当前观测。
6. 输出 WinINET `observed` 和原生 `reportedExit`。两者各自必须是公网 IPv4，但不要求互相相等，也不与任何预设地址比较。
7. 保留 Pause 阻断、保护下 retry 的同 logical-session/新 attempt/相同监听器、Disconnect、父进程 EOF、崩溃重启、外部配置变更保留及最终恢复检查。

原生字段目前仍叫 `verifiedExitIpv4`，本脚本只把它当作实际观测值；这个历史名称不代表
expected-IP 验证。探测超时会记录 `detectorAdvisory=true` 以及最近核对的 connected 状态，
并令本次验收报告“未验证到观测报告”。轮询函数不会因此 Pause/Disconnect。测试最终的
正常清理仍关闭自己启动的 host、恢复自己的配置；这不属于产品的探测失败策略。

10808 不作写入目标，不假定存在监听器或旧 PID。即使开始时 ProxyEnable=0 而
ProxyServer 仍保存 `127.0.0.1:10808`，也按完整元组原样恢复，不擅自清空遗留字符串。
本任务没有运行此 live 脚本；实际运行由主任务在 native 重建后协调。

## 离线脚本回归

```powershell
& .\Windows\scripts\ablate-observed-exit.ps1 -ScriptOnly
```

脚本内嵌 Python unittest，只执行生产 Python 文件中的函数定义，跳过顶层 DLL 和账号
加载器。WinINET、native 进程、时间和联网边界均由内存夹具代替，不读真实账号、不修改系统配置。

5 项测试覆盖：

- subscription/asset 的完整生命周期编排，profile 没有 expected 字段、资源身份完整保留。
- WinINET 返回 `8.8.8.8`，原生报告 `9.9.9.9` 时通过，证明没有新增地址相等条件。
- 连接后延迟到来的当前尝试观测被读取。
- 缺失观测有时间边界且轮询不发送生命周期命令。
- 迟到尝试和非公网结果被拒绝。

最初红灯明确捕获了旧脚本仍发送 expected-IP 字段；修改后 5 项通过。夹具中的完整恢复
流程只验证脚本编排，不能作为真实 WinINET、崩溃恢复或系统代理行为已经通过的证据。

## 两项行为消融

```powershell
& .\Windows\scripts\ablate-observed-exit.ps1
```

默认工具路径：Go `D:\Program Files\Go\bin\go.exe`，Dart/Flutter 位于 `E:\tools\flutter`，
Python `C:\msys64\ucrt64\bin\python.exe`。可通过具名参数指定现有工具，不安装工具。

每次创建独立 `Windows/.artifacts/ablation/observed-exit-<时间>-<随机ID>/workspace`，
复制所需源码和测试。Flutter 使用该副本中的离线依赖解析，并检查三个本地包的解析路径
均留在副本内。每个实验执行 baseline → 删除行为 → 指定断言失败 → 原字节恢复 → 重测。
编译错误、测试加载失败或无关错误不能计为成功消融。

| 消融 | 真实行为测试 | 必须捕获的失败 | 结论 |
| --- | --- | --- | --- |
| 仅删除 `Controller.run` 中 ProbeExit 完成后的 `currentLocked(a)` 检查 | `TestObservedExitCancellationCannotPublishLateAddress` | `late observation survived cancellation` | 保留当前尝试的观测发布检查 |
| 删除 GUI `_readObservedExit` 对 `actualExitIp` 的赋值 | `connection observation is advisory and context bound: changed-exit-system-proxy` | 预期实际观测 `8.8.8.8`，GUI 却仍为 null | 保留 native snapshot 到 GUI 的观测更新 |

两项都不针对已删除的 expected-IP guard，也不支持恢复旧的 expected-IP 准入要求。
原生正向基线还覆盖无 expected 字段、legacy 元数据不影响准入、探测失败或无效观测保持连接。
GUI 正向基线覆盖 pending/changed/diagnostic/failure/stale 等观测状态。

每次目录保留 `script-contract.log`、`native-{baseline,ablated,restored}.log`、
`gui-{baseline,ablated,restored}.log`、离线依赖日志、`source-sha256.json` 和 `results.json`。
末尾会重新比较共享源码哈希；若其他负责人期间修改了输入，脚本不把旧快照标为当前成功，要求重跑。
实验只改副本，所有产物保留用于复核；没有 Git 操作、服务/防火墙操作或 live 系统代理运行。

## 阶段运行记录：最终冻结后须重跑

以下运行在当时快照上完成，但主任务随后发现 diagnostic 成功重试后的观测发布缺口，
Poincare 正在更新 controller/nativehost，另有 app 测试时序调整。因此以下目录仅为阶段证据，
不代表最终集成源码已冻结或通过。遵照主任务要求，等待源码冻结后再执行最终隔离消融；
主任务可直接运行本文件给出的完整脚本命令，无需额外改测试入口。

阶段运行目录：

```text
Windows/.artifacts/ablation/observed-exit-20261012-191319-ff949ac2425d4bff83d2768b0db6928a/
```

结果：

| 检查 | 结果 |
| --- | --- |
| Python 离线脚本行为测试 | 5 项通过，完整生命周期分别运行 subscription/asset 夹具 |
| 原生 `^TestObservedExit` 基线及恢复 | 4 项顶层测试通过，另含 legacy/失败结果子用例 |
| 原生删除当前尝试检查 | 指定测试以 `late observation survived cancellation` 失败；paused 快照错误保留迟到 IP |
| GUI 观测基线及恢复 | 每轮 9 项通过 |
| GUI 删除实际观测赋值 | 指定测试以 `Expected: '8.8.8.8' / Actual: <null>` 失败 |
| 共享源码输入哈希 | 本轮结束时全部匹配，`sourceSnapshotCurrent=true`；后续 diagnostic 修改须重跑 |
| Python / PowerShell 语法 | 通过；Python 模块未被执行 |
| 真实系统代理 / 服务 / 防火墙 / 10808 | 未运行或修改 |

额外负例已确认 Python `IPv4Address.is_global` 本身不能排除组播地址，且构造器会接受
整数地址；脚本现明确拒绝组播和非字符串观测。该修正先观察了两个失败断言，再通过复测。

19:09 的早一轮也捕获了两个预期行为失败，但源文件在运行期间被其他负责人修改，
快照校验返回失败并记录 `sourceSnapshotCurrent=false`。该旧目录保留为过程证据，
不作为当前源码验证结果。上面的 19:13 完整重跑确认脚本能够执行两个消融，但因随后新增的
diagnostic 修正，也不能替代最终冻结后的那一轮验证。

这证明阶段快照中的两个边界的回归测试能发现对应行为被删除。没有证明真实公网探测、WinINET
恢复、系统代理联网、安装签名或整套 Windows Beta 验收已完成。

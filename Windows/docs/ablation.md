# Windows 消融实验

> 本文保留历次实验的历史结果。2026-10-12 用户取消了 Windows 的固定预期出口门槛；早期“必须保留出口匹配”结论依赖已被替代的要求，不再适用。当前行为见[连接后出口检测](observed-exit-flow.md)，新消融见[连接后检测消融](observed-exit-ablation.md)。

日期：2026-10-11。实验使用相同的功能回归，临时删除安全机制后必须观察到行为断言失败，再恢复实现。没有把编译失败计作有效消融，也没有将代码行减少推断为性能提升。

## 实际简化

| 删除内容 | 原因 | 对照验证 |
| --- | --- | --- |
| UI `_listGeneration` 及重复比较 | `loading` 已串行化分页，账号 `_generation` 已隔离退出/换号；第二组代次没有新增保护 | 简化前后同一组 15 项 UI/控制器测试均通过；独立审阅确认该前提 |
| API 适配器重复 IPv4 校验代码 | 与领域判断重复，且允许范围不一致 | 原 65 项测试简化前后均通过，增加私网/保留地址用例后 68 项通过 |
| Go attempt 重复保存的完整 Context 和重复检查 | 唯一 active attempt 加取消上下文已覆盖身份；重复状态增加不同步的可能 | 删除后完整功能回归、race、vet 通过 |

没有增加通用 Repository、全局服务容器、插件工厂或为未来协议准备的多级转发层。保留 CustomerDataSource、SessionStore、HttpTransport 与 Engine 边界，因为当前实际适配器或错误注入测试需要它们。

## 不能删除的安全机制

| 临时删除 | 观察到的回归 | 结论 |
| --- | --- | --- |
| 领域固定出口检查、到期检查 | 错误资源变为可准备连接 | 保留 |
| 原生事件代次检查 | 旧事件影响新会话 | 保留 |
| 精确整数计数与采样连续性 | 大计数精度丢失，缺样本被算成正常速度 | 保留 |
| 提醒实际批次成员限制 | 未提交的提醒成员被错误标记 | 保留 |
| API 单航班续期、持久重试记录、代次隔离、恢复单航班 | 重复刷新、未保存请求 ID、旧令牌覆盖或恢复提前完成 | 保留 |
| Go 出口匹配与 attempt 隔离 | 错误出口开门、旧探测放行新会话 | 保留 |
| UI 连接取消检查 | 已取消的详情响应将状态从 idle 改为 connected | 保留 |

共 13 项安全消融，均触发原有行为回归。恢复后各模块重新通过完整测试。UI 测试随后增加审阅回归，因此最终测试数高于消融基线。

详细临时差异、命令与结果：

- [领域 6 项实验](../packages/domain/ABLATION.md)
- [API 4 项实验及简化前后结果](../packages/data_source/docs/verification.md)
- [Go 2 项实验及简化结果](../native/core/verification.md)
- UI：`scripts/ablate-ui.ps1`；日志 `.artifacts/verification/ui-ablation-{baseline,no-attempt-guard,restored}.log`。

这些实验验证行为必要性与等价简化，不是吞吐量、CPU、内存或真实网络泄漏测试。

## 后续真实内核增量

新增独立副本实验 `scripts/ablate-native.ps1`：删除网关的迟到拨号检查后，原测试观察到关闭门后的连接被放行；恢复后 8 项 localengine 回归通过，实际源码未被变体修改。连同前述 13 项，已执行 14 项安全消融。

实际删除了 nativehost 中仅转发 `context.Background()` 的无必要包装函数；修改前后 localengine/nativehost 同一组 15 项测试通过。没有因为已连接一次就删除出口、身份、期限或取消检查。

## 2026-10-12 系统代理增量

`scripts/ablate-system-proxy.ps1` 新增三项独立副本实验：代理应用失败后的关门、EOF 恢复、
持久化后的设置归属重查。三种删除均成功编译且触发原行为回归，恢复后通过；累计 17 项。
最新范围与原始证据位置见 [系统代理验证](system-proxy-verification.md)。不把文件占用、编译失败
或测试进程启动失败计为消融有效结果。

## 2026-10-12 Beta 集成增量

新增更新器签名与下载完整性两项消融，以及 `scripts/ablate-beta.ps1` 的三项边界消融，
累计执行 **22 项**。每项均先通过基线、成功编译变体、观察到预期行为断言失败，再恢复
通过；临时副本和恢复后的哈希均已检查。

| 删除机制 | 实际回归 | 处理 |
| --- | --- | --- |
| Ed25519 清单签名验证 | 被篡改清单被接受 | 保留 |
| 下载完整性验证 | 错误内容被视为可安装包 | 保留 |
| broker 父 GUI 发布者检查 | 未受信调用方被接受 | 保留 |
| Host.Close 保留 TUN 保护 | 进程关闭解除持久保护 | 保留 |
| 服务端时钟不得向后校准 | 旧响应延长已知资格期限 | 保留 |

证据：`.artifacts/ablation/beta-20261012-141933/`、
`.artifacts/verification/beta-ablation.log` 和
[更新器消融记录](update-install-verification.md)。这些结果不代替真实提权 WFP、
重启、网络切换或安装验收。

同时删除 TUN 配置中没有实际作用的 `NeverResolve` 字段：入口已由严格策略解析为
字面公共 IPv4。两个引擎重复的 HTTPS 出口请求、响应解析和总超时逻辑合并到
`native/core/exitprobe`，保持首个可靠错误出口立即失败的规则。相应引擎测试、race 和
vet 通过；这些属于等价简化，没有据代码行减少宣称吞吐量或延迟提升。

随后执行 `scripts/ablate-controller.ps1`，再增加两项隔离副本实验：移除更新构建号
检查使同一版本错误触发恢复；反转诊断会话比较使旧会话结果进入当前展示。两项均成功
编译并触发预期行为断言，恢复后通过；累计 **24 项**。副本结束时对照共享控制器的
SHA-256 确认源码一致，证据位于 `.artifacts/ablation/controller-20261012-153954/`
及 `.artifacts/verification/controller-ablation.log`。

协议集成还移除了 nativehost 对“旧 Auth 与显式配置冲突”的重复分支，该规则已统一由
`engine.Profile.EffectiveProtocol()` 执行，控制器及宿主都调用同一校验。删除前后同一组
`go test -tags with_gvisor ./nativehost -run '^TestProfile' -count=1` 均通过，仍拒绝冲突
凭据、缺失参数和不支持的字段。证据为 `profile-simplification-before.log` 与
`profile-simplification-after.log`；共享规则本身保留。

最终集成又增加一项“删除已接受尝试的资源身份同步”实验：先连接 A，再切到 B，
让 B 的第一次出口验证失败。删除同步后重试使用旧资源身份并失败，恢复后同一测试通过。
累计 **25 项不同的行为消融**。本轮同时重跑更新构建号和诊断 context 两项，三项均为
基线通过、可编译变体触发预期行为失败、恢复通过；重复执行不重复计数。证据：
`.artifacts/ablation/controller-20261012-164531/results.json`、
`.artifacts/verification/controller-ablation-final.log`。

GUI 物理绑定 HTTPS 集成后，删除 `IoHttpTransport.resolveHost` 及其只替换 DNS 结果的
socket 分支。该分支已无生产调用，且不能证明 socket 绑定物理网卡；相关取消、无回退、
主机名等验证由新原生传输测试覆盖。数据源保留的 162 项测试与静态分析通过，生产 API
登录/读取另经真实 GUI 平台通道验证。这是移除重复路径，不宣称性能收益。

# ADR 0011：Windows 后台服务采用 Go

- 状态：Go 服务和共享原生层决定继续有效；内核集成部分于 2026-10-19 由 [ADR 0049](0049-xray-only-four-platforms.md) 修订为 Xray-core。
- 原决策日期：2026-10-10，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-11。

## 当前决定

1. Windows 10/11 后台服务采用 Go，在受控服务/原生宿主中嵌入 Xray-core；界面通过可信 IPC 提交结构化请求。固定版本与模块见[共享手册](../development/four-platform-development.md)。
2. `Windows/native/core/` 保存可跨平台复用的配置校验、Xray 适配、入口解析策略、出口观测与暂停控制；Windows 服务、WFP、系统代理、修复及网络变化处理保持平台边界。
3. 本地代理固定 `127.0.0.1:12808`，保留现有系统代理归属、暂停与恢复语义；TUN/WFP 保护不因更换内核降低要求。全局接管仍需要可信安装及服务权限。
4. Android 通过 Go 原生库与 Kotlin VpnService 复用共享部分，负责系统授权、前台生命周期、protect、底层 Network 绑定和 fd 所有权；不再采用旧 libbox 方案。iOS/macOS 通过原生 Packet Tunnel 桥接，见各平台手册。
5. 账号登录、API 会话与 token 留在业务层。原生层只执行结构化资源配置、有效期与保护约束；实际出口在连接后观测，失败不拆除可用连接，不恢复旧预期出口放行门槛。
6. 配置、生命周期、能力和平台接口保持分层，但唯一代理内核为 Xray。保留接口不等于允许第二内核或 sing-box 回退。

## 权限与生命周期

- 高权限服务只接受经过身份验证的结构化参数，不能接受任意命令或路径。服务不可用、版本不兼容或驱动失败应明确报错，不能报告虚假已连接。
- 内核与宿主处于同一进程，其异常可能影响宿主；WFP 保护意图、服务恢复及独立网络修复必须按自身生命周期验证，不能依赖 GUI 或 Xray 存活维持全部保护。
- Windows 的签名提权、崩溃、重启、恢复及多用户归属需要实机证据；跨平台 Go 单元测试不足以证明这些行为。
- Win7 继续按 [D-01](0001-win7-delivery-scope.md)独立验证，本项不确认主力版 Go 服务或该 Xray 包直接兼容 Win7。

## 源码依据与历史

当前源码依据为固定提交的 [core/xray.go](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/core/xray.go)，提供实例创建、启动与关闭。模块及平台兼容性仍须通过当前构建和运行验证。

2026-10-10 曾以 sing-box `testing` 提交 `6afeff4c0f7123b5782f888812e96b8c82c7b699` 的 `Box/New/Start/Close` 作为原 Go 内嵌选择依据。此项仅保留历史来源，已不作为当前依赖或接口要求；旧测试结果不迁移到 Xray。

执行入口：[Windows 手册](../development/windows.md)、[Android 手册](../development/android.md)、[iOS 手册](../development/ios.md)、[macOS 手册](../development/macos.md)。

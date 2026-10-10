# ADR 0011：Windows 后台服务采用 Go

- 状态：已接受。
- 决策日期：2026-10-10，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-11。
- 确认依据：用户选择 A，Windows 后台服务使用 Go，沿用内嵌 sing-box 与共享原生层方案。

## 决定

1. Windows 10/11 后台服务采用 Go，在受控服务进程中内嵌 sing-box；界面通过鉴权 IPC 提交结构化请求。
2. `native/core` 保存可跨平台复用的请求校验、协议配置生成、内核适配、入口解析策略、出口检测和转发门槛。
3. `native/windows-service` 负责 Windows 服务控制、命名管道、WFP、TUN 集成、网络变化和保护意图；Windows 网络修复工具由 `native/netrepair` 提供。
4. Android 通过 Go/libbox 封装复用共享部分，Kotlin VpnService 负责授权、前台运行、底层网络绑定及系统生命周期。Windows 专用规则不进入 Android 共享实现。
5. 账号登录、网页/V2 取数和业务资格判断继续通过 Dart 业务层执行。原生服务自行执行已下发的有效期、出口验证和保护约束，不持有业务登录令牌。
6. 内核适配接口保持通用，首版仅实现 sing-box（D-02）。Flutter 到原生层的具体平台通道或 FFI 方案由 S6 验证，本项不预先决定桥接方式。

## 权限与生命周期

- Windows 服务按安装阶段取得的权限运行，界面在用户会话中操作系统代理；高权限服务只接受结构化参数，配置和内部路径由服务生成。
- 运行中不因每次切换 TUN 再申请 UAC；服务不可用、版本不兼容或驱动失败时按对应错误处理，不显示虚假的已连接状态。
- 内核与 Go 服务处于同一进程，内核异常可能影响整个服务。保护规则的生命周期、服务恢复和独立网络修复工具须按 D-06 验证，不能依赖界面或内核进程存活才能保持保护。
- Win7 的路线和兼容性按 D-01 独立验证；本项不等于确认主力版 Go 服务可直接运行于 Win7。

## 上游依据与验证边界

2026-10-10 核对上游 `testing` 分支提交 `6afeff4c0f7123b5782f888812e96b8c82c7b699`：

- [go.mod](https://github.com/SagerNet/sing-box/blob/6afeff4c0f7123b5782f888812e96b8c82c7b699/go.mod) 定义 Go 模块 `github.com/sagernet/sing-box`。
- [box.go](https://github.com/SagerNet/sing-box/blob/6afeff4c0f7123b5782f888812e96b8c82c7b699/box.go) 提供 `Box`、`New`、`Start` 和 `Close`，作为 Go 内嵌方案的源码依据。

此提交仅用于记录当前源码证据，不作为已选发行版本。S4 仍须锁定稳定内核版本、Go 工具链和构建选项，验证启停、资源清理、暂停监听、检测门槛及两种 Windows 模式；S5 验证服务终止和内核异常时的保护，S7 验证 Android 集成。

## 关联文档

- [主设计](../superpowers/specs/2026-10-10-sunipip-client-design.md)
- [设计审阅报告](../superpowers/reviews/2026-10-10-sunipip-client-design-review.md)
- [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)
- [实施计划](../superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)

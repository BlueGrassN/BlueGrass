# ADR 0002：主力客户端首版仅集成 sing-box（已被替代）

- 状态：已被 [ADR 0049：四端仅使用 Xray-core](0049-xray-only-four-platforms.md) 于 2026-10-19 全文替代。
- 决策日期：2026-10-10，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-02。
- 确认依据：用户选择 A，首版只集成 sing-box，保留通用内核适配接口。

> 以下保留 2026-10-10 的历史决定及其理由，不再指导当前实现。Windows、Android、iOS、macOS 均仅使用 Xray-core；不保留旧核心回退或第二内核。当前执行入口为[四端开发手册](../development/four-platform-development.md)。

## 历史背景

主设计以 sing-box 为主内核，并预留 Xray 适配。需要明确首版是否实际交付第二内核，以及保留哪些扩展边界。

## 决定

1. Windows 10/11 与安卓主力客户端首版仅集成 sing-box。
2. 保留通用内核适配接口，由适配层负责内核配置生成、启动与停止、状态和流量事件、能力检查及错误转换。
3. 首版只有 sing-box 适配实现，不打包 Xray、不提供双内核切换。业务模型和界面通过通用接口访问内核能力。
4. 后续有明确需求时，可增加 Xray 等内核适配，并另行规划实现和验证。
5. Win7 的内核路线独立处理，继续遵循主设计和 ADR 0001 中的安排。

## 备选方案

首版同时集成 sing-box 和 Xray：未采用，需要额外维护配置生成、运行生命周期、发行包及兼容性测试。

## 影响

- 首版内核配置、构建与兼容性测试围绕锁定版本的 sing-box 展开。
- 通用内核接口属于当前设计范围；Xray 专用适配实现属于后续扩展。
- 本项只确定内核范围。首版启用哪些代理协议由 D-03 单独决定，不因选定 sing-box 自动确定协议清单。

## 关联文档

- [主设计](../superpowers/specs/2026-10-10-sunipip-client-design.md)
- [设计审阅报告](../superpowers/reviews/2026-10-10-sunipip-client-design-review.md)
- [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)
- [实施计划](../superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)
- [ADR 0001：Win7 交付范围](0001-win7-delivery-scope.md)

# Shared Client Architecture Implementation Plan

**Goal:** 将跨端公共代码移到根目录，并让 Android、Windows、macOS 消费同一 Flutter 客户端库及实际共享的业务/UI 功能。
**Architecture:** 根 `packages/domain`、`packages/data_source`、`packages/client` 与 `core`；原平台工程保留原生宿主和薄入口，以适配器连接共享功能。
**Tech Stack:** Flutter/Dart、Go/Xray、Kotlin、C++、Swift、GitHub Actions。
**Spec:** [已确认设计](../specs/2026-11-06-shared-client-architecture-design.md)。

## Global Constraints

保留所有现有用户功能、账号与会话隔离、网络保护/恢复语义。保持包 ID、签名、Go 模块路径与 Xray 锁。公共源码和品牌不得复制成多份独立实现。继续使用固定 `preview` Release 和递增平台构建号。不能把构建通过声称为设备联网验收。

## Tasks

- [x] 基线测试、移动 domain/data_source/core；建立共享 Flutter 包及平台导出入口，修正全部执行路径与依赖。
- [x] 共享 Android/桌面可复用控制逻辑及稳定能力接口；保留各平台连接与恢复状态机，增加真实行为测试。
- [x] 合并公共主题、页面内容、组件与响应式导航；移动平台实现到共享库内的明确适配边界，保留专属功能并验证两类布局。
- [x] 更新所有构建、测试、工具链与 GitHub 路径触发，更新 README、平台手册、共享架构手册和 AGENTS.md。
- [x] 整体回归、独立审阅、打包与 CI 验证；推送 main 并同步 E:/workspace/APP，记录实际发布与未覆盖范围。

已完成结果见 [迁移验证记录](../../development/shared-client-verification-20261106.md)：三端 main 构建成功，固定 preview 的 12 个附件及来源验证通过；真实设备联网与权限恢复仍按平台手册验收。

## Review Focus

相对路径/fixture/许可证路径不能在新检出或 Linux/macOS runner 失效；共享状态不得泄漏不同账号或旧连接事件；手机端不得调用桌面 IPC/WFP；桌面不得丢失已有恢复/更新/提醒；平台导出层不能形成源码复制或循环依赖。

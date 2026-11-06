# Routing auto update implementation plan

**Goal:** Android 自动获取并使用 Loyalsoldier 更新，不需要重新安装 APK。

**Architecture:** 共享 Go 规则导入/更新缓存 + Android Go JNI 不可变会话快照 + Kotlin JobScheduler/通道 + Flutter 状态卡。内置数据是恢复基线。

**Spec:** ../specs/2026-10-24-routing-auto-update-design.md

- [x] Go：先添加坏输入、原子缓存、版本/并发与会话快照回归，再实现受限官方 HTTPS 下载、状态及 JNI；保持原路由契约。
- [x] Kotlin：独立实现联网每日后台调度、启动补查、手动更新通道、生命周期取消与真实设备测试。
- [x] Flutter：独立实现规则更新状态模型、设置卡、手动更新和状态刷新；有错误/窄屏/异步测试。
- [x] 集成审查并运行 Flutter、Go race/vet、Android instrumentation；修复所有实际问题。
- [x] 重建 ARM64/x86_64 AAR 与正常 APK，模拟器验证官方下载/缓存恢复和两模式网络功能，更新依赖与验收记录。

主任务负责共享 Go 与 JNI、集成、打包、设备验收；Kotlin 与 Flutter 工作按目录分离。使用现有脏工作区，保留历史验收及未提交工作，不创建丢失未跟踪 Android/Windows 源码的新 checkout。

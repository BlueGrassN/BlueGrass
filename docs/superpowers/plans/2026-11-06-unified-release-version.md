# Unified Release Version Implementation Plan

> **For agentic workers:** Use the existing isolated checkout, parallelize platform version wiring from publisher implementation, and review the integrated change before main publication.

**Goal:** 所有可交付端统一 0.1.0/X.X.X，Release 和 Git 发布标签仅保留 preview。
**Architecture:** 根 release.json 唯一公开版本；平台保留内部构建号；三端成功后整体发布不可变的统一版本。
**Tech Stack:** Python、PowerShell、Flutter/Dart、GitHub Actions。
**Spec:** [已确认需求与实现约束](../specs/2026-11-06-unified-release-version-design.md)。

## Global Constraints

公开版本严格为无前导零的 X.X.X；包内显示、发行文件名与页面一致。保留 Android 签名/versionCode、Windows buildBase、Mac build、全部来源和哈希校验。README 不纳入 Git。无需重置工作流编号或重写历史源码。

## Tasks

- [x] 盘点、保存清理记录，删除非 preview Release/附件及旧平台 Git 标签，并核对 preview 标签指向与原附件未变。
- [x] 平台版本：根配置接入三端构建和路径触发；移除公开后缀/硬编码；更新相关行为测试，执行平台打包工具与 Flutter 回归。
- [x] 发布器：先补行为测试覆盖旧状态迁移、等待全端、不匹配版本/来源/回退拒绝、同版本幂等、整组失败保留与恢复；实现纯版本命名和整体切换；运行发布测试及 actionlint。
- [x] 同步共享发布手册、三端发布/开发文档、AGENTS 和本地 README；独立审阅；预演后推送 main，运行三端 CI 与统一发布。
- [x] 核对只剩 preview、所有应用/附件/页面版本为 0.1.0、12 个附件哈希/来源正确；同步原目录并保留本地 README 与既有修改，记录实际验收边界。

实际构建、包内版本与发布证据见 [统一版本验证记录](../../development/unified-version-verification-20261106.md)。

## Review Focus

不能把旧 APK/ZIP 改名冒充新版本；内部构建号不得回退；纯版本稳定文件名不能覆盖不同字节；单端失败不能产生混合版本页面；旧成功运行或迟到工作流不能倒退共同版本；所有下载内容只作数据解析。

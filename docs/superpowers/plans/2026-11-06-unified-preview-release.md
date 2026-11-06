# Unified Preview Release Implementation Plan

**Goal:** 固定 `preview` Release 集中各端最新成功安装包。
**Architecture:** 原有工作流只构建，新增串行发布工作流扫描三端成功运行，验证 artifact 后合并发布。
**Tech Stack:** GitHub Actions、GitHub CLI、Python 标准库。
**Spec:** [设计](../specs/2026-11-06-unified-preview-release-design.md)。

## Global Constraints

保持各端版本号、构建号及签名方式；只接受当前仓库 main 的 push/workflow_dispatch 成功运行；禁止 PR artifact 发布。保留历史 Release；不覆盖私密文件和现有本地工作。

## Review Focus

旧运行晚完成不能倒退；单端失败不能清空其他端；同名校验/元数据不能互相覆盖；上传失败不能先删除旧包；macOS 两架构必须同一成功运行。

## Tasks

- [x] 新增 `scripts/tests/test_publish_preview.py`，先验证运行选择、artifact 身份/哈希、合并和幂等失败场景。
- [x] 实现 `scripts/publish_preview.py`；增添 `.github/workflows/publish-preview.yml`，移除 Windows/Android 平台 Release job，保留所有构建检查。
- [x] 更新根 README、三端发布/开发文档、共享开发手册，新增根 `AGENTS.md` 固化统一入口与验证要求。
- [x] 执行 Python 测试和工作流检查；真实 artifact 只读预演；独立代码审阅。
- [x] 提交推送 main，运行统一发布工作流，验证四个安装包与元数据；将改动同步回 APP 主目录。

## 本地验证

15 项 Python 行为测试通过；actionlint 1.7.7 通过，仅豁免已实际使用的 macos-15-intel runner 标签。真实 artifact 只读预演验证 Windows +27、Android +100002、macOS +14 两架构。独立审阅提出的草稿查找、失败事件排队、空 starter 上传恢复及清理状态交叉保护已修复并验证。

## 真实发布验证

首次运行暴露草稿创建后立即按 tag/list 重新查找失败，已改为使用创建响应的 Release ID，并用 CLI 的 pending-tag 查询发现已有草稿；上传和编辑直接使用 ID。修复工作流 [42171910566](https://github.com/BlueGrassN/BlueGrass/actions/runs/42171910566) 成功。固定 [preview 页面](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview) 已公开为预发布，四个安装包和全部 12 个附件校验通过，安装包摘要与原发布包一致。主目录文件已同步，原有 `.gitattributes` 本地注释差异保留。

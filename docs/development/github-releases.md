# 统一 GitHub 开发预览版

更新：2026-11-06。下载入口固定为 [BlueGrass 开发预览版](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)，tag 为 `preview`，标记为 Pre-release。Windows x64、Android ARM64、macOS arm64/x86_64 共用一个公开版本，起始为 `0.1.0`。iOS/HarmonyOS 尚无可下载安装包。

## 公共版本

根 `release.json` 的 `version` 是唯一发行版本配置，严格使用无前导零的 `X.X.X`。更新时只修改该值，例如从 `0.1.0` 改为 `0.1.1`；Windows、Android、macOS 的构建入口读取它，注入包内版本与公共 Flutter 显示。不要手改平台 pubspec 或旧包文件名来代替实际构建。

系统内部构建号独立递增：Windows 为 `Windows/release.json` 的 `buildBase + github.run_number`；Android 为 `100000 + github.run_number`；Mac 为 `github.run_number`。这些值用于系统升级、验证与追溯，不拼入公开版本。保留原有工作流名称、编号、签名和应用身份。

公开附件示例为 `BlueGrass-Windows-0.1.0-x64.zip`、`BlueGrass-Android-0.1.0-arm64.apk`、`BlueGrass-macOS-0.1.0-arm64.zip`。不添加 beta、dev、+build 或 attempt。校验文件和元数据使用同一平台与版本前缀；真实构建号、commit、run、attempt 保留在元数据或来源链接中。

一个已经完整发布的版本不可覆盖。相同版本的重跑保持现有安装包；需要发布修复或新代码时先递增公共版本，再推送 main，触发三端重建。发布器拒绝共同版本回退，且不会用更旧的工作流运行替换新产物。

## 构建与整组发布

`windows-release.yml`、`android-release.yml`、`macos-dev.yml` 只负责构建、测试和上传 artifact。根 `release.json` 改动触发三端；只有 `publish-preview.yml` / `scripts/publish_preview.py` 更新 Release。

发布器每次检查全部三端，只接受本仓库 main 的成功 push/manual 运行，验证构建提交仍在 main 历史中、该提交的根版本配置和包元数据都等于目标版本。Windows 另校验内部构建号，Android 要求固定签名和 `sourceDirty: false`；macOS 两种架构必须来自同一次成功运行。

新版本必须等 Windows、Android、Mac 双架构全部通过，才整体更新页面和下载。某端未完成、失败或版本不符时保留上一整组下载，避免混合版本。旧格式状态可以读取；迁移时重新构建全部安装包，不将旧包改名冒充统一版本。

运行列表只按 main 分支查询，由发布器逐条校验 completed/success 和来源。GitHub API 的 `status=success` 索引曾遗漏刚完成的成功运行，因此不用它做预筛选。确认更新时核对页面逐端 commit/run 与附件摘要，不能仅依据任务成功状态。

只有合格发布 job 进入 `unified-preview-publish` 共享并发锁，正在上传的任务不会被取消。每次汇总完整目标版本，因此被合并的待执行通知不会丢失单端更新。构建 job 保持只读权限；统一发布 job 才有 contents:write 和 actions:read。

## 事务与来源

先验证全部来源和哈希，在隐藏 pendingRelease 中记录未完成事务，再上传并核对整组新附件；完成后切换页面及已发布状态，最后清理旧托管附件。事务记录只更新隐藏数据，不改变旧下载表或草稿状态。上传中断后，重跑先按记录核对摘要/大小，只清理尚未发布的本次暂存附件，然后重新选择合格构建；因此同版本出现更新的运行或目标版本递增也可恢复，不留下未归属文件。写事务记录前先完整检查既存同名附件；异内容冲突保持零写入，不会污染以后版本的恢复。恢复时摘要不符的暂存附件也会被拒绝，不静默覆盖或删除。已发布版本的清理中断仍保留 retiredAssets 清单；当前有效附件不能列入删除清单。

版本统一不改变实际来源。各端页面仍分别提供 commit 和 run 链接；固定 preview tag 的 Source code 压缩包不代表所有安装包源码。Windows 构建后的 sourceDirty 标记如实保留。Windows 未签名、Android 既有 Alpha 签名、macOS ad hoc/未公证状态保持原事实，设备 VPN/TUN、真实账号联网和权限恢复仍按平台手册验收。

2026-11-06 按用户要求删除了 4 个非 preview Release 及其 15 个附件。发行页面只保留 preview；不再生成平台 Release/tag。Git 源码标签未纳入此次产物清理。README 仅在本地维护，不上传到仓库或 Release。

## 维护与验证

已有新版本三端成功构建但需要补发时，无需重新编译：

```powershell
gh workflow run publish-preview.yml --repo BlueGrassN/BlueGrass --ref main
```

只读预演与行为测试：

```powershell
python scripts/publish_preview.py --repo BlueGrassN/BlueGrass --dry-run --work-dir .artifacts/preview-dry-run
python -m unittest discover -s scripts/tests -v
```

预演的 waiting 表示目标版本仍缺少合格平台构建；它不会改写当前下载。updated 只有在整组准备好后才包含全部平台；已发布相同版本时为空。errors 必须为空。工作流同时运行 actionlint。

实际写入始终通过 Actions 获取共享锁；本地只用 --dry-run。保护说明末尾的 bluegrass-preview-state（含 pendingRelease 事务记录）：状态缺失或损坏时停止，不按空状态覆盖。不要手改托管附件、重置工作流编号、上传账号/密钥/token/私密配置或测试日志。Actions artifact 保留 14 天；待发布文件过期时需重新构建。私有仓库下载仍需访问权限。

新增平台须扩展整组选择、校验、命名和行为测试，并使用同一公共版本，不能恢复独立平台 tag。参考：[GitHub 并发规则](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)、[workflow_run 事件](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run)。

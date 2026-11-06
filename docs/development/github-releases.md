# 统一 GitHub 开发预览版

更新：2026-11-06。下载入口固定为 [BlueGrass 开发预览版](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)，tag 为 `preview`，标记为 Pre-release。Windows x64、Android ARM64、macOS arm64/x86_64 的最新成功安装包全部显示在此页。iOS/HarmonyOS 尚无可下载安装包。

## 构建和发布

| 工作流 | 职责 |
| --- | --- |
| `windows-release.yml` | 按原有路径触发，测试、打包并上传 Windows artifact |
| `android-release.yml` | main 推送时测试、签名、打包并上传 Android artifact |
| `macos-dev.yml` | 按原有路径触发，验证并上传 macOS 两种架构 artifact |
| `publish-preview.yml` | 接收上述成功运行，统一更新 `preview`；也支持 main 手动运行 |

各端构建工作流名称保持不变，避免重置运行编号。平台流程不再创建 `windows-v…`、`android-v…`、`macos-v…` Release/tag。已有历史记录保留，供核对旧包与源码。

统一发布器每次扫描**全部三端**的成功运行，只接受本仓库 `main` 的 `push` / `workflow_dispatch`，拒绝 PR、fork 和非主分支产物，并确认构建提交仍在 main 历史中。按 `run_number`、`run_attempt` 选择最新成功运行，不按完成时间倒退。某端失败、尚未完成或没有更高编号的成功构建时，继续保留该端已经发布的安装包。macOS 两架构必须来自同一次成功运行。

运行列表只按 `main` 分支查询，由发布器逐条核验 `completed/success` 和来源；不使用 GitHub API 的 `status=success` 筛选索引。2026-11-06 已观察到该索引遗漏刚成功的 macOS 运行，而普通分支列表能返回它，导致发布任务成功但页面仍保留旧包。确认更新时检查页面的逐端 commit/run 与附件摘要；需要补发时手动运行统一发布工作流，无需重建安装包。

只有符合条件的发布 job 进入同一并发组，失败构建或 PR 通知不会占用该锁；正在上传的任务不会被取消。GitHub 默认可能替换同组旧的 pending 任务；由于每次都会重新检查全部平台，后续发布仍会收集所有已完成更新。只有统一发布 job 拥有 `contents: write` 和 `actions: read`；构建仍使用只读仓库权限，Android keystore 只提供给原有签名构建步骤。

## 附件和来源

安装包名称带平台、客户端版本、构建号和运行 attempt，例如 `BlueGrass-Android-0.5.0+100002-r1-arm64.apk`。同一平台的校验文件、构建信息和 macOS 使用说明采用相同前缀，不会发生各端 `build-info.json`、`SHA256SUMS.txt` 重名。

发布器重新验证包 SHA256、构建元数据、源码提交和平台约束；Windows 还核对对应提交中的 `Windows/release.json`，Android 仍要求固定签名和 `sourceDirty: false`。Windows 构建信息中的 `sourceDirty` 是现有打包脚本在构建后记录的事实，原样保留，不伪称干净源码证明。构建信息保留 artifact 原始文件名，Release 中的实际文件名以页面下载链接和该前缀的校验文件为准。

先上传并校验整组新附件，再更新页面及其隐藏状态，最后清理该发布器管理的旧附件。上传中断时旧下载仍在；清理中断会保留待清理清单，重跑继续处理。同名文件只在摘要、大小完全相同时复用；不静默覆盖不同内容。GitHub 上传失败遗留的空 `starter` 附件，仅在尚未被已发布状态引用时删除重试。清理状态若把当前有效附件标成待删除，则拒绝处理。失败上传产生的尚未引用附件可能暂时可见，页面表格仍指向上次完整发布的下载。

**各平台版本号和源码提交可以不同。** 页面逐端展示版本、commit 和构建链接。`preview` tag 在首次创建时固定到一个提交，不强制移动；GitHub 自动生成的 Source code 压缩包不是所有安装包的共同源码。追溯源码必须点击对应平台的 commit。

Windows 未签名、Android 当前 Alpha 签名及 macOS ad hoc/未公证状态保持原有事实；统一展示不等于正式版发布，也不会启用客户端自动更新。设备 VPN/TUN、真实账号联网和权限恢复仍按各平台验收文档测试。

## 维护与验证

重新汇总现有成功构建，无需重新编译所有端：

```powershell
gh workflow run publish-preview.yml --repo BlueGrassN/BlueGrass --ref main
```

只读预演会下载和校验真实 artifact，但不修改 GitHub：

```powershell
python scripts/publish_preview.py --repo BlueGrassN/BlueGrass --dry-run --work-dir .artifacts/preview-dry-run
python -m unittest discover -s scripts/tests -v
```

实际写入统一通过上述 Actions 工作流执行，以获得仓库级串行锁。不要同时从本地直接运行写入模式。不要手动删除 Release 说明末尾的 `bluegrass-preview-state` 隐藏状态；状态缺失或损坏时发布器会停止，不会当作空状态覆盖已有内容。不要手改托管附件或重置各端构建工作流的运行编号。

Actions artifact 保留 14 天。已发布附件继续保留到对应平台成功更新；如果待首次发布的 artifact 已过期，需要手动重跑该平台构建。当前仓库为私有，下载者仍需要仓库访问权限。

新增平台时统一扩展 `scripts/publish_preview.py` 的选择、验证、附件命名及页面表格，补充行为测试；不得恢复按平台创建独立 Release 的方式。

参考：[GitHub 并发规则](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)、[workflow_run 事件](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run)。

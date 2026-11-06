# APP 仓库协作规则

## 结构与改动范围

- `Windows/`、`Android/`、`Mac/`、`iOS/`、`HarmonyOS/` 为平台目录；`Windows/packages/` 和 `Windows/native/core/` 有跨端依赖。
- `client-api/` 是独立服务端，`client-config/` 是共享公开配置，`assets/branding/` 保存原始图片，`docs/development/` 保存开发手册。
- 开始前检查 Git 状态和最新远端。保留已有未提交工作；跨任务共享目录时核对具体文件，必要时使用隔离工作区。
- 原始品牌图片及锁定哈希的上游文件必须保持字节不变，遵守 `.gitattributes`；不要通过重算哈希掩盖换行转换或意外修改。

## GitHub Release：统一入口

- 用户选择固定一个 Release 集中所有平台最新安装包。唯一自动发布入口是 `preview`，标题 `BlueGrass 开发预览版`。
- 先读 [统一发布手册](docs/development/github-releases.md)。平台工作流只构建和上传 artifact，只有 `.github/workflows/publish-preview.yml` / `scripts/publish_preview.py` 更新 Release。
- 不再为 Windows、Android、macOS 或新增平台生成独立发布 tag。已有历史 Release/tag 保留，未经明确要求不删除。
- 发布器必须只接受本仓库 main 的成功 push/manual 运行，拒绝 PR/fork/非主分支产物；下载的 artifact 只能作为数据，不得执行。
- 每次串行汇总全部平台，旧运行不得回退已发布新版本。单端失败保留该端原包，不能清空或覆盖其他端附件。macOS 两架构必须同一成功运行。
- 先验证来源和哈希、上传完整新附件，再更新页面和状态，最后清理旧托管附件。附件命名包含平台，校验和与元数据不能重名；保留幂等重跑和失败恢复。
- 各端版本/构建号独立；不要重置工作流编号或强行统一版本。`preview` tag 不是所有安装包的共同源码，页面必须逐端给出真实 commit/run 链接。
- 保留签名、sourceDirty、设备测试等实际边界，不把预览版或 CI 成功描述成正式签名、公证或真实联网验收完成。
- 实际发布使用 Actions 的共享并发锁；本地只读检查加 `--dry-run`。保护 `bluegrass-preview-state` 状态及原有签名 Secrets，不将账号、密钥、token、私密配置或测试日志上传。

## 验证与文档

- 修改统一发布代码后执行 `python -m unittest discover -s scripts/tests -v`；对工作流运行 actionlint。发布器变化应补充能验证合并、回退防护、来源/哈希拒绝或失败恢复的行为测试。
- 推送前运行 `python scripts/publish_preview.py --repo BlueGrassN/BlueGrass --dry-run` 核对真实 artifact；网络/过期限制必须如实报告。
- 发布行为变化须同步根 README、共享发布手册、相应平台 `docs/github-release.md` / `Mac/docs/TESTING.md`、`docs/development/` 平台入口及本文件。
- 构建入口和既有测试要求见各端开发手册。仅修改发布流程时执行相应发布测试和工作流检查，不以此声称已重新验证所有客户端运行时行为。

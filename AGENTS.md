# APP 仓库协作规则

## 结构与改动范围

- `Windows/`、`Android/`、`Mac/`、`iOS/`、`HarmonyOS/` 为平台宿主目录；公共实现位于根 `packages/domain`、`packages/data_source`、`packages/client`、`core` 与 `contracts`。
- `client-api/` 是独立服务端，`client-config/` 是共享公开配置，`assets/branding/` 保存原始图片，`docs/development/` 保存开发手册。
- 各级目录的 README 文件仅在本地维护，不纳入 Git，也不能用强制添加绕过忽略规则；已有跟踪文件只解除跟踪，保留本地内容。
- 开始前检查 Git 状态和最新远端。保留已有未提交工作；跨任务共享目录时核对具体文件，必要时使用隔离工作区。
- 原始品牌图片及锁定哈希的上游文件必须保持字节不变，遵守 `.gitattributes`；不要通过重算哈希掩盖换行转换或意外修改。

## 跨端共享开发

- 先读 [共享客户端架构](docs/development/shared-client-architecture.md)。所有新公共 UI、业务协调、主题/组件在 `packages/client` 实现；Windows/app/lib 和 Android/lib 只保留薄入口/兼容导出，不再维护独立实现副本。
- 三端宿主共同依赖 `bluegrass_client`；Windows 宿主包名 `bluegrass_windows` 不改变 BlueGrass 可执行文件名，Android/Mac 应用身份保持原值。保留原有签名、内部构建编号和协议契约。
- 根 `core/` 的 Go module 仍为 `bluegrass/native/core`；Android 桥接通过 replace 引用它，不能再创建平台私有的公共核心副本。
- 保留各端原生网络事务、账号/请求世代隔离、暂停/恢复保护和权限边界。共同控制逻辑通过接口/组合调用平台适配器，不能以界面复用为由删掉桌面更新、提醒、崩溃记录或 Android VPN 生命周期。
- Flutter 统一使用 3.47.9 / Dart 3.13.7；涉及公共包修改时运行其分析/测试及所有受影响宿主回归。packages、core、contracts 改动必须触发相应平台 CI。
- 更新相对路径时同时检查测试 fixture、构建/消融脚本、许可证、CI 缓存和锁文件。搬移的品牌/许可证/规则字节必须一致。

## GitHub Release：统一入口

- 用户选择固定一个 Release 集中所有平台最新安装包。唯一自动发布入口是 `preview`，标题 `BlueGrass 开发预览版`。
- 先读 [统一发布手册](docs/development/github-releases.md)。平台工作流只构建和上传 artifact，只有 `.github/workflows/publish-preview.yml` / `scripts/publish_preview.py` 更新 Release。
- 不再为任何平台生成独立发布 tag。用户已要求仅保留 preview 的发行产物；非 preview Release/附件已清理，Git 源码标签不在本次清理范围。
- 发布器必须只接受本仓库 main 的成功 push/manual 运行，拒绝 PR/fork/非主分支产物；下载的 artifact 只能作为数据，不得执行。
- 获取 main 运行列表后自行核验 completed/success，不能依赖 GitHub 的 status=success 筛选索引；该索引可能遗漏刚完成的成功运行。以 Release 逐端来源和附件哈希确认实际更新。
- 每次串行汇总全部平台，目标版本须三端全部成功后整体发布；任何单端缺失或失败均保留上一整组下载。macOS 两架构必须同一成功运行，不能出现混合公开版本或旧运行回退。
- 先验证来源和哈希，记录隐藏 pendingRelease 事务，再上传完整新附件、切换页面及已发布状态，最后清理旧托管附件。恢复时仅清理事务记录中未发布且哈希/大小一致的暂存附件，不能删除当前有效包或无关附件。附件命名包含平台，校验和与元数据不能重名；保留幂等重跑和失败恢复。
- 根 release.json 是唯一公开版本配置，严格使用无前导零的 X.X.X；应用显示、包名和 Release 版本一致，不带 beta/dev/+build/attempt。发布新内容必须先递增公共版本；已完整发布的同版本文件不可覆盖，重跑仅恢复或保持现有发布。
- 内部构建号保留平台递增规则，不重置工作流编号或签名。构建脚本和路径过滤必须消费根 release.json；发布器核对对应源码配置和包内元数据。preview tag 不是所有安装包的共同源码，页面逐端提供真实 commit/run。
- 保留签名、sourceDirty、设备测试等实际边界，不把预览版或 CI 成功描述成正式签名、公证或真实联网验收完成。
- 实际发布使用 Actions 的共享并发锁；本地只读检查加 `--dry-run`。保护 `bluegrass-preview-state` 状态及原有签名 Secrets，不将账号、密钥、token、私密配置或测试日志上传。

## 验证与文档

- 修改统一发布代码后执行 `python -m unittest discover -s scripts/tests -v`；对工作流运行 actionlint。发布器变化应补充能验证合并、回退防护、来源/哈希拒绝或失败恢复的行为测试。
- 推送前运行 `python scripts/publish_preview.py --repo BlueGrassN/BlueGrass --dry-run` 核对真实 artifact；网络/过期限制必须如实报告。
- 发布行为变化须同步根 README（仅本地维护）、共享发布手册、相应平台 `docs/github-release.md` / `Mac/docs/TESTING.md`、`docs/development/` 平台入口及本文件。
- 构建入口和既有测试要求见各端开发手册。仅修改发布流程时执行相应发布测试和工作流检查，不以此声称已重新验证所有客户端运行时行为。

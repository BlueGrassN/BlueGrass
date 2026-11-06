# Windows GitHub 构建与发布计划

日期：2026-11-06。用户已授权修改、推送 main，并完成首次 GitHub Release。

目标：本地和 GitHub 使用同一打包入口。本地生成并解压测试客户端；main 上 Windows 相关代码变更自动检查、编译，成功后发布可下载预发布包。现有 macOS 工作流保持独立。

方案：复用现有 Flutter/Go 构建与载荷校验，新增 Windows Actions 工作流。仅上传 Actions artifact 不能满足下载 Release 的要求；另外维护一套云端打包命令会让本地与云端产物分叉，因此采用共享脚本加独立发布 job。

- [ ] 使 PATH、windres 和 CPython 查找兼容本机与 windows-2022 runner；固定已有 Flutter/Go 版本。
- [ ] 新增 release.json 和 package.ps1，运行检查、构建、解压、包校验、无联网助手检查，并输出 ZIP、SHA256SUMS、来源元数据。
- [ ] 新增 push/PR/manual 工作流；PR 只验证，main 成功后发布独立版本的预发布 Release。构建 job 只读，发布 job 才有 contents:write。
- [ ] 本地运行共享入口与工作流静态验证，确认 testaccount.txt 不被跟踪、包内没有测试绑定或凭据。
- [ ] 提交推送，跟踪实际 Actions，修复出现的问题，确认 Release 资产及其来源提交。

版本来源为 Windows/release.json；本地默认 buildBase，云端为 buildBase + run_number。超过 Windows 版本字段上限则明确失败。Release 标签包含构建号与重跑次数，不覆盖既有版本。

检查重点：全新 checkout 不依赖本机缓存文件；Action 选择的工具优先于系统预装工具；任何检查失败不能发布；只下载当前 run 的确切产物；旧客户端运行时不得先删一半旧解压目录。继续保护 10808，不运行真实账号/TUN 接管测试。

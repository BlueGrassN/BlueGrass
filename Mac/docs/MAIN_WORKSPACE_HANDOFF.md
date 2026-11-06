# Mac 主目录移交记录

日期：2026-11-06。Mac 开发成果已同步到 `E:/workspace/APP`，后续由用户在主目录统一切换 `main`、提交和上传 GitHub。

## 移交内容

- `Mac/`：共享 UI 启动器、Swift 宿主与系统代理组件、构建脚本、测试、品牌资源和使用说明。
- `Windows/app/`、`Windows/native/core/` 中的 Mac 适配：Mac 与 Windows 共用页面、业务控制器和 Go/Xray 核心，因此这些改动也是 Mac 工程的组成部分。
- 上一轮已验证的 Flutter 3.47.9、Dart 3.13.7 和依赖升级，包含四个锁文件与两个应用根目录的精确版本覆盖。
- `.github/workflows/macos-dev.yml`：自动构建分支已改为 `main`，保留手动触发、Apple Silicon/Intel 双架构、Node.js 24 Actions 及依赖锁文件检查。
- `docs/development/macos*.md` 与 Mac 专属方案/计划：保留构建证据和设计说明。
- `Mac/.gitignore`：排除开发安装包及 Swift 缓存，源码上传不需要包含本地构建产物。

共享代码按实际文件差异逐项审查后移交。Go 系统拨号器由 `dialer_windows.go` 改名为 `dialer_desktop.go`，构建范围为 Windows 和 Darwin。共享协议测试仅带回已在 Mac CI 验证过的 TUIC/AnyTLS 拒绝断言，保留主目录原有格式和其余测试内容。

## Git 操作边界

同步时主目录分支为 `feature/windows-client`，HEAD 为 `3a4a513d605c18973b0b9555e19acee5de131340`。本次只移交工作文件，没有切换分支、暂存、提交、合并或推送。用户可以连同其他平台的工作一起处理根仓库。

移交来源为 Mac 开发工作树提交 `c3a36f837013c10c7aa4af5d4e36baf24a7c61f6`。其中实际通过 GitHub 双架构构建的源码提交为 `7e2221e02f5279724724719c1d03d2ed255dfbb9`，测试包版本为 `0.1.0-dev+10`；详见[依赖升级与构建记录](../../docs/development/macos-dependency-refresh-20261106.md)。

上传 `main` 后，相关源码变化会触发 `macOS development build`。工作流位于 GitHub 默认分支后，也可在 Actions 中选择 `Run workflow`。本次主目录移交没有触发新的 GitHub 构建。

## 主目录验证

验证直接在 `E:/workspace/APP` 进行，使用 `E:/tools/flutter-3.47.9`：

- 四个 Dart/Flutter 项目均通过锁定依赖安装，锁文件与移交版本一致。
- 两个 Flutter 应用和两个 Dart 包静态分析均无问题。
- 共享 Flutter 测试：437 项；Mac 启动器：3 项；domain：164 项；data_source：179 项，全部通过。
- Go 全模块测试通过。首次运行的各包断言通过，但临时测试 EXE 清理被文件占用中断；使用本次验证专属临时目录复跑后，命令正常退出。
- Darwin arm64/amd64 原生宿主与 TUN 组件交叉编译、相关包 `go vet` 通过。交叉编译不替代实际 Mac 执行。
- Python 打包/签名哈希测试：11 项通过；共享品牌资源校验通过。
- Actionlint 通过；仅沿用已在 GitHub 成功使用的 `macos-15-intel` runner 标签例外，因为本地 actionlint 1.7.7 的标签表较旧。

真实 Mac 的系统密码弹窗、实际线路联网、全局路由/DNS 接管和恢复，仍按[使用与测试说明](TESTING.md)验收。

## 本地备份和范围核对

备份与逐文件清单位于 `E:/workspace/APP/.artifacts/mac-handoff-20261106-c3a36f8/`，该目录已被根目录现有忽略规则排除。

- `original/`：覆盖或改名前的 43 个文件原件。
- `plan.json`、`receipt.json`：移交文件范围、来源和 SHA-256。
- `destination-before.json`：同步前的文件哈希、分支、HEAD 和暂存差异摘要。
- 验证日志保存在同一目录。其他 1106 个原有文件的内容哈希保持一致；根目录 `.gitignore`、原有分支和暂存区保持原状。

原 Mac 工作树和此前测试包继续保留，便于核对历史构建。

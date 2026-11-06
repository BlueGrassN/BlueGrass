# Android 本地构建与 GitHub Release

## 仓库结构与依赖

| 根目录 | 用途 |
| --- | --- |
| `Android/` | Flutter 界面、Kotlin VPN、Go Android 桥接及 APK 构建 |
| `Windows/` | Windows 客户端，以及 Android 共用的 Dart packages 和 Go core |
| `Mac/`、`iOS/`、`HarmonyOS/` | 其他平台工程或资料 |
| `client-api/` | 独立客户端 API |
| `client-config/` | 客户端公共配置与 bootstrap |
| `assets/` | 公共品牌资源 |
| `docs/` | 设计、开发和验收记录 |
| `.github/workflows/` | Android 发布及原有 macOS 构建 |

必须 checkout **完整仓库**。`Android/pubspec.yaml` 引用 `../Windows/packages/`，`Android/native/core/go.mod` 引用 `../../../Windows/native/core`。AAR、APK、local.properties、Gradle wrapper 和密钥不依赖提交：Flutter 会补齐 wrapper，脚本会从 Go 源码重建 AAR。

## 本地一键生成测试客户端

在仓库根目录使用 PowerShell 7：

```powershell
./Android/scripts/build-release.ps1
```

依赖：Flutter **3.47.7**、Go **1.27.2**、JDK **17**、Python 3；SDK platform/build-tools **36 / 36.0.0**、NDK **28.2.13676358**。Flutter、Go、Python 在 PATH 中，`JAVA_HOME` 与 `ANDROID_HOME` 指向工具目录；也可传 `-JavaHome`、`-AndroidSdk`、`-FlutterExecutable`、`-GoExecutable`、`-PythonExecutable`。

脚本先清理 Flutter 旧产物，按锁文件恢复依赖，执行 Flutter analyze/test、Go 测试/vet，再重建原生库和 ARM64 Release APK。Go 使用只读模块解析，下载依赖保留正常的 go.sum 校验；CI 另检查依赖锁未变化。不会打开 Android Studio 或模拟器。设备 VPN 转发验收仍使用原有单独的测试脚本。

输出：

- `Android/build/app/outputs/flutter-apk/app-release.apk`：Flutter 标准产物。
- `Android/dist/<版本>+<构建号>/BlueGrass-Android-<版本>+<构建号>-arm64.apk`：可分发文件。
- 同目录 `SHA256SUMS`、`build-info.json`：文件校验和、签名证书指纹、源码提交及原生库清单。

本地有未提交内容时，构建信息会标记 `sourceDirty: true`，其中的提交 SHA 仅代表基线；GitHub 发布只接受干净检出的源码。

无签名环境变量时沿用本机 debug 密钥，便于本地测试；这是 Release 优化模式的测试签名包。提供以下四个变量则使用指定密钥：`BLUEGRASS_ANDROID_KEYSTORE`（绝对路径）、`BLUEGRASS_ANDROID_STORE_PASSWORD`、`BLUEGRASS_ANDROID_KEY_ALIAS`、`BLUEGRASS_ANDROID_KEY_PASSWORD`。`-RequireReleaseSigning` 强制要求完整配置。

## GitHub 一次性配置

2026-11-06：`BlueGrassN/BlueGrass` 已配置以下四个 Secrets，无需再手工提供。当前 Alpha 沿用既有测试包的证书，SHA256 指纹为 `34068810c741fefa5be8c3b59f3850fed6ce10224fe0688f6ac8545be4ed0061`，后续 GitHub 包可以覆盖安装既有 Alpha。私钥在仓库外另存为使用随机密码保护的 PKCS12，密码由本机 Windows DPAPI 保护；私钥和密码均不进入 Git。下面的步骤用于其他仓库或后续更换发行密钥。

仓库：`BlueGrassN/BlueGrass`。在 **Settings → Secrets and variables → Actions → New repository secret** 配置：

| Secret | 内容 |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | 持久保存的签名 keystore 文件的 Base64 内容 |
| `ANDROID_KEYSTORE_PASSWORD` | keystore 密码 |
| `ANDROID_KEY_ALIAS` | 签名 key 的 alias |
| `ANDROID_KEY_PASSWORD` | 该 key 的密码 |

使用已有发行密钥，或在首次正式分发前创建并备份一个固定密钥。可用 JDK 自带 keytool 交互输入密码，密钥保存在仓库外：

```powershell
keytool -genkeypair -v -keystore 'E:/private/bluegrass-android.jks' -alias bluegrass -keyalg RSA -keysize 3072 -validity 10000
# 只复制到剪贴板后粘贴到对应 Secret，不打印到日志、不提交文件。
[Convert]::ToBase64String([IO.File]::ReadAllBytes('E:/private/bluegrass-android.jks')) | Set-Clipboard
```

父目录需要预先存在。密钥必须长期保留，不能在每次 CI 运行时随机生成。**覆盖安装要求签名一致**：现有本地包使用测试密钥；若 GitHub 改用新发行密钥，旧测试包需要卸载后首次安装。后续固定密钥的 GitHub 包可以正常升级。需要从 CI 包切回本地包时，使用同一发行密钥和不低于已安装版本的 `-BuildNumber`；默认本地构建号较低。

未配置或少配任一 Secret，主分支工作流会在开头明确失败，不会把临时签名包发布到 Release。`GITHUB_TOKEN` 由 Actions 自动提供，无需配置个人令牌；发布 job 单独申请 `contents: write`。仓库/组织须允许 Actions 和该写权限。密钥仅写入 runner 临时目录并在任务结束时清理。

## 推送后的行为

配置好 Secrets 并将流程文件随代码推送后：

1. **每次 push 到 `main`**：测试 → 编译 → APK 校验 → 上传 Actions artifact → 创建 GitHub Release。
2. **Pull Request 到 `main`**：执行同样构建检查，上传测试签名的 APK artifact；不读取发行密钥、不创建 Release。
3. **手动运行**：Actions → Android build and release → Run workflow。选择 `main` 会发布；其他分支只生成测试 artifact。

客户端版本名读取 `Android/pubspec.yaml`；CI `versionCode = 100000 + github.run_number`，因此无需每次推送手改版本号。重跑同一次 run 保持 versionCode；Release tag 包含 attempt，例如 `android-v0.5.0-build.100001-r1`，不会覆盖以前的 Release。不要删除重建工作流导致 run_number 重置；如更换编号策略，应保持 versionCode 递增。

当前客户端仍是 Alpha，自动创建 **Pre-release**，不抢占其他平台的正式 Latest。先上传至 Draft，文件全部就绪后才公开。每次主分支运行独立排队，不会因后续推送主动取消；同一个 PR 的旧检查会被新检查替换。工作流失败时不会发布新的完整下载包，旧 Release 仍可下载。

下载入口：[GitHub Releases](https://github.com/BlueGrassN/BlueGrass/releases)。展开对应 Android 版本的 **Assets**，下载 `.apk`，不要下载自动生成的 Source code 压缩包。Actions artifact 仅保留 14 天；Release 附件不采用这个自动过期设置。

## 校验与边界

产物检查包括应用 ID、版本号、最低 API 26、非 debuggable、仅 ARM64、原生库压缩、签名、ZIP 完整性及 ZIP/ELF 16KB 对齐。CI 不会启动 VPN、使用真实账号或运行设备网络验收；Release 说明会标明这一点。

实现参考：[GitHub workflow 语法](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)、[Flutter Android 签名与发布](https://docs.flutter.dev/deployment/android)、[GitHub CLI Release](https://cli.github.com/manual/gh_release_create)。

本轮实际本地构建、全新源码副本及边界见[2026-11-06 验证记录](github-release-verification-20261106.md)。

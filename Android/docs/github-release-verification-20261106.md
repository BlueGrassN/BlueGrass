# Android 自动构建流程验证（2026-11-06）

## 本地一键构建

实际运行 `Android/scripts/build-release.ps1`，完成：

- Flutter analyze 无问题，164 项 Flutter 测试通过。
- Android Go bridge 及共享 routingpolicy、routingupdate、protocolconfig 测试通过，相关 go vet 通过。
- 从源码重建 ARM64 AAR，检查固定 Xray 版本及仅 ARM64 的原生库。
- Release APK 签名、应用 ID/版本、API 26、非 debuggable、仅 ARM64、原生库压缩、ZIP 完整性和 ZIP/ELF 16KB 对齐检查通过。
- Dart/Go 依赖锁文件未改变。

产物：`Android/dist/0.5.0+9/BlueGrass-Android-0.5.0+9-arm64.apk`，**20,539,017 bytes**。

SHA256：`d683e622ef643b1555e84d004d5a9e3b71bead10fb4ee28b65575c35b263c033`。

本机测试签名证书 SHA256 保持 `34068810c741fefa5be8c3b59f3850fed6ce10224fe0688f6ac8545be4ed0061`。Flutter 应用 AOT 与 Flutter 引擎库和上一版相同；Go 库本次重新编译，二进制哈希不同，不将其宣称为字节相同。源码基线 `a703404bc844bbc51fe0ce92b81d7080b3470946`，含本轮未提交修改，构建信息明确标记 `sourceDirty: true`。

## 工作流及失败分支

- actionlint 1.7.12 检查 `android-release.yml` 通过，PowerShell/Python 语法检查通过。
- 缺失发行签名、只提供部分签名变量：构建脚本在编译前明确拒绝。
- 给 APK 检查器传入错误版本：在导出任何分发文件前拒绝。
- 新 Windows clone 复现了 10 份规则许可证的 CRLF 哈希不匹配；增加 `.gitattributes` 字节保留规则后，通过 Git 的 checkout filter 验证 10 份哈希均匹配。
- 独立代码审查指出并修复：许可证换行、仅重试发布任务时产物名称不匹配、同组并发队列取消中间推送。构建 job 输出真实 artifact 名称，发布 job 直接引用；主分支使用每次 run 独立的并发组。

日志及原始结果位于本机忽略目录 `Android/.artifacts/github-release-20261106/`。首次尝试中 `go mod verify` 对仓库内本地替换模块报缺失 archive hash，失败日志保留；最终流程使用只读模块编译/测试、正常 go.sum 下载校验及依赖锁检查。

## 全新源码副本与指定签名

从本仓库 HEAD 创建本地独立 clone，并仅覆盖本轮构建流程修改。开始时明确检查 AAR、local.properties、gradlew.bat 和 gradle-wrapper.jar 均不存在；不复制任何先前生成的构建产物。修正 Git checkout 的许可证字节后，运行同一脚本，加 `-RequireReleaseSigning -BuildNumber 100001`。

这次再度通过 analyze、164 项 Flutter 测试、所选 Go 测试/vet、AAR 构建及全部 APK 检查。Flutter 自动生成 wrapper 和 local.properties；Gradle daemon 使用 JDK 17。指定签名分支使用现有本机测试密钥验证，未创建或上传正式发行密钥。

该验证包为 **0.5.0+100001**，**20,543,697 bytes**，SHA256 `ab80691b38bfbd8cfecd7a6c9c099bf710b13abd0af7d3d9d7bfadac831cf89a`。测试副本位于 `.artifacts/android-ci-checkout-20261106/`，不会覆盖常规本地分发目录中的 0.5.0+9 测试包。这验证了首次源码构建和显式签名路径，仍使用本机工具/下载缓存，不等同于 GitHub 托管 runner 验收。

## 验证边界

本轮未提交或推送代码，未创建远端 Release，未配置仓库 Secrets，因此不能将本地结果称为 GitHub 托管运行成功。启用流程须按 [配置说明](github-release.md) 配置四个固定签名 Secrets 并推送流程文件。

未打开 Android Studio/模拟器，未执行新的设备 VPN 转发测试；本轮修改涉及打包与发布流程，现有应用功能验收记录仍独立保留。

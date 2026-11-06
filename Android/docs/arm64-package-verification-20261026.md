# ARM64 安装包检查（2026-10-26）

本记录对应压缩前的 53.9 MB 产物；当前进一步压缩后的安装包见
[原生库压缩检查](arm64-compression-verification-20261026.md)。下列旧包校验值保留为历史证据。

按用户要求，Android 原生构建入口、兼容入口和 Gradle ABI 过滤统一改为仅 ARM64
（`arm64-v8a`）。手机安装包使用 Release 模式，版本保持 **0.3.2+6**。

## 体积与产物

| 产物 | 字节 | 十进制 MB | MiB |
| --- | ---: | ---: | ---: |
| 原双架构 Debug APK | 231,374,871 | 231.37 | 220.66 |
| 新 ARM64 Release APK | 53,897,261 | 53.90 | 51.40 |
| 新 ARM64 AAR | 12,956,416 | 12.96 | 12.36 |

APK 减少 **76.71%**。当前包只有三份 ARM64 ELF：`libapp.so`、`libflutter.so`、
`libbluegrass.so`，无 x86/x86_64、Dart 调试 kernel/snapshot、Vulkan 验证层。
ZIP 文件开销降至 51,456 bytes，最大文件间隙为 8,192 bytes，不再含原包约 38 MB 的冗余空间。

- APK：`build/app/outputs/flutter-apk/app-release.apk`
- APK SHA-256：`a1516580e8be1e663e25d3efe361ca12f480589399b3f1e3cd1535f0aa786fd9`
- AAR SHA-256：`1f7d729977c2bfe949887f2684d9dc94cdad91865ff88c2ca41c5b7ceb76a5de`

## 构建与检查

已完成原生 ARM64 构建、Flutter clean、锁定依赖恢复、Release 构建。
首次使用 `--no-pub` 时，Flutter 3.47.7 跳过按 Release 模式生成插件注册文件，导致旧注册文件
引用已被 Release 排除的 `integration_test`。改用正常 Release 构建命令后通过，没有修改 SDK
或手工编辑生成文件；依赖锁文件保持不变。README 已记录对应构建命令。

- `aapt`：仅 `arm64-v8a`，版本 0.3.2/6，minSdk26、targetSdk36，未启用 debuggable。
- `zipalign -c -P 16 4`：通过。
- `apksigner verify`：通过，签名身份与原 APK 相同；继续沿用现有 debug 证书，Release 指优化构建模式。
- AAR 中 ARM64 原生库字节与先前经过功能验收的版本完全一致，Java 绑定 class 内容也完全一致。
- Release DEX 中保留 `go.Seq`、`Bridge`、`Platform`、`Session` 等 JNI 入口，AAR 保留相应混淆保护规则。
- 实际 APK 原生库内检出正确的内置 GeoSite/GeoIP 数据，Go 模块信息确认锁定的 Xray 版本，无第二代理内核。
- `native/core/dependencies.txt` 已更新为最终 ARM64 AAR 的实际依赖信息。

本轮为架构和打包检查，未重新启动 Android Studio/模拟器，未宣称新 Release APK 已在 ARM64
设备完成运行验收。先前分流和规则自动更新的功能证据保留在
[0.3.2 功能报告](rules-auto-update-verification-20261024.md)。

本轮日志、体积明细、签名检查和旧包备份在 `.artifacts/arm64-20261026/`。

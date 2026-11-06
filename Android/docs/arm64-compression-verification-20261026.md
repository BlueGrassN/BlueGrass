# ARM64 原生库压缩检查（2026-10-26）

为继续缩小直接分发的 APK，在 Gradle 中启用 `packaging.jniLibs.useLegacyPackaging = true`，
使原生库在 APK 内使用 DEFLATE 压缩、由 Android 安装器解压。版本仍为 **0.3.2+6**，仅 ARM64。

| 产物 | 字节 | 十进制 MB | MiB |
| --- | ---: | ---: | ---: |
| 上一版 ARM64 Release | 53,897,261 | 53.90 | 51.40 |
| 当前压缩 ARM64 Release | 20,505,917 | 20.51 | 19.56 |

本次再减少 **61.95%**；相较最初 231,374,871-byte 双架构调试包，共减少 **91.14%**。

| 原生库 | 未压缩字节 | APK 内压缩字节 |
| --- | ---: | ---: |
| libapp.so | 5,702,536 | 2,303,586 |
| libbluegrass.so | 35,938,064 | 12,246,518 |
| libflutter.so | 11,747,864 | 5,478,843 |

## 验证结果

- 干净 Release 构建完成，依赖锁文件不变。
- ZIP 完整性检查通过；仅有 ARM64 三份原生库，均为 DEFLATE 压缩。
- 三份库解压后均与上一版逐字节一致；所有 DEX、assets、res 和 resources.arsc 内容也一致。
- 三份 ELF 的架构与 16KB 加载段对齐检查通过。
- 最终合并 Manifest 确认 `extractNativeLibs=true`，上一版为 false；新包未启用 debuggable，minSdk26、targetSdk36。
- APK 对齐及签名校验通过，签名身份与上一版相同，仍沿用现有 debug 证书。
- ZIP 结构开销仅 19,628 bytes，没有增量打包的大块冗余。

本次缩小的是直接下载的 APK。安装时会另外保存约 53.4 MB 解压后的原生库，因此安装占用
可能增加，不能把 APK 的减少量当作安装后节省量。APK 与解压库合计约 73.9 MB，尚未计入
ART、缓存和数据，实际占用由设备决定。机制见
[Android 官方原生库打包说明](https://developer.android.com/guide/topics/manifest/application-element#extractNativeLibs)。

本轮完成构建与产物检查，未重新开启 Android Studio/模拟器，未新增 ARM64 设备运行结果。

## 交付

- APK：`build/app/outputs/flutter-apk/app-release.apk`
- SHA-256：`35467fb38f2dadd29dca6e5aedbb988daa2b0ef9872076d2cb4ab0c79ddd7c1f`
- 日志、逐文件校验及压缩前备份：`.artifacts/arm64-compressed-20261026/`

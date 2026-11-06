# Windows 界面对齐与 Android Emulator 验证

本页保留 0.2.0+2 的历史 UI/fixture 验证。后续已完成正常账号与真实资源测试、修复 UDP 中继问题并生成 0.2.1+3，当前 APK 及结果以[真实功能验收](live-functional-verification-20261024.md)为准。

日期：2026-10-24，Asia/Hong_Kong。本轮使用已安装的 Android Studio、Android SDK Emulator 与 ADB 实际运行 Android 应用，不以没有 USB 手机为由跳过设备测试。

## 界面与修复

以当前 `Windows/app/lib/main.dart` 与 `app_theme.dart` 为准，统一蓝白/深色配色、品牌、4px 卡片边框、登录文案、官网入口、首页连接卡、“我的 IP”、设置及诊断信息结构；手机使用底部导航、48px 触控尺寸与可滚动布局。Android 未实现的 Windows 专属开关没有伪造。

官网按钮经原生 Intent 打开固定的 `https://sunipip.com/`，不接受任意外部 URL。诊断导出仅包含允许的状态字段，不含节点密码、令牌或资源身份。

最终正常 APK 安装后已启动到登录页，并实际点击“注册账号”；系统切换到 `org.chromium.webview_shell/.WebViewBrowserActivity`，Intent 的目标为 `https://sunipip.com/`。返回后检查无遗留 BlueGrassVpnService。正常入口截图见[生产入口登录页](previews/emulator-20261024/production-login.png)。

实际 API 适配器使用 `api_v2`，原安卓恢复逻辑错误地要求 `apiV2`，导致连接快照被丢弃。本轮先复现失败，再修复并验证 connecting/connected/recover。独立审阅另外修复了恢复未知资源时误用预选资源名称的情况，以及测试异常退出后未释放 VPN 的风险；定向复核无剩余可操作问题。

## 运行环境与证据

- Android Studio 已打开 `E:\workspace\APP\Android` 工程；使用已有 `gst_x64` AVD，Android 11 / API 30 / x86_64，1080×2280，WHPX 加速，设备 `emulator-5554`。
- Flutter 3.47.7，Go 1.27 工具链，JDK 25.0.3，NDK 28.2.13676358。原生库重建为 `arm64-v8a` 和 `x86_64`，未使用 10 月 13 日的旧 sing-box AAR。
- Xray 模块固定为 `v1.260327.1-0.20261017152356-92ccc939aaf4`；构建脚本核对每个 ABI 的实际 ELF、Go 构建信息和核心依赖，拒绝链接 sing-box。

| 验证 | 结果 | 实际覆盖 |
| --- | --- | --- |
| Flutter analyze | 无问题 | 当前 Dart 源码与集成测试 |
| Flutter 测试 | **102 项通过** | 页面、窄屏/键盘/大字、主题、资源/出口对应、会话/API/平台通道 |
| Android instrumentation | **52 项全部在模拟器通过** | 16 项安全存储、27 项状态机、6 项 Android 平台契约、3 项真实 JNI/Xray 加载及校验 |
| Flutter emulator integration | **完整流程通过** | 真实 Android 上的登录表单、首页、我的 IP、设置、诊断、主题和系统 VPN 弹窗 |
| 原生库构建 | 两种 ABI 成功 | Xray 版本、JNI 绑定、无第二代理内核 |

设备测试使用编译出的正常 APK 与 instrumentation APK，通过 `adb shell am instrument -w -r com.bluegrass.client.test/android.test.InstrumentationTestRunner` 执行，最终输出 `OK (52 tests)`。Gradle 的额外 UTP 协调器首次下载较慢，因此采用同一测试 APK 的标准设备 runner；没有把仅编译成功计为运行通过。

系统流程实际验证：

1. 点击 Android 系统 VPN 弹窗的拒绝按钮，原生状态保持 idle。
2. 再次连接并点击允许按钮，真实创建 VpnService/TUN；使用故意不可解析的 `.invalid` 测试入口验证启动失败后处于 paused，原生 `tunActive=true`。
3. 在真实界面主动断开，确认原生 idle、`tunActive=false`，连接按钮可再次使用。
4. 测试 teardown 会限时断开，宿主脚本 finally 停止该测试应用并重置其 VPN 授权，避免留下阻断网络的 TUN。

账号与资源为明确 fixture，VPN 通道和系统弹窗为真实原生调用。本轮没有生产账号登录、可用商业节点的 TCP/UDP 转发联调或实际出口验证；也没有把模拟器结果扩展为手机厂商后台/锁屏行为已通过。

## 复现

正常入口最终 APK：`build/app/outputs/flutter-apk/app-debug.apk`，**208,300,125 bytes**，SHA-256 `099C1C7441147D534BA7E63503A9D1D6671F63046BEFE57DED42E95CDF49F34C`。已安装到模拟器，包名 `com.bluegrass.client`，版本 `0.2.0+2`，minSdk 26、targetSdk 36，含 ARM64/x86_64。`zipalign -c -P 16 4` 通过；从 `lib/main.dart` 构建，包中未出现集成测试入口标记。

原生 AAR：`android/app/libs/bluegrass.aar`，24,998,784 bytes，SHA-256 `77BF7055714FFE97B8029454F7EE57239D779DF7EB82CF3411513E70AA9A69BB`。APK 内原生库大小为 ARM64 32,937,776 bytes、x86_64 35,197,232 bytes，与构建产物对应。

在 Android Studio 的 Device Manager 启动 `gst_x64` 后，于 `Android/` 执行：

```powershell
.\scripts\build-native.ps1
flutter pub get
.\scripts\test-emulator.ps1 -Device emulator-5554
```

原生 instrumentation 也可由 Android Studio 运行，或先在 `android/` 生成 `:app:assembleDebugAndroidTest -Ptarget-platform=android-x64`，再用 ADB 安装正常 APK 与测试 APK，并执行上面的 runner。

本轮 Google Storage 的 Java 下载路径很慢，构建过程临时使用 `FLUTTER_STORAGE_BASE_URL=https://storage.flutter-io.cn`；依据 [Flutter 官方镜像说明](https://docs.flutter.dev/community/china)，只在当前构建进程设置，没有修改全局镜像或降低 TLS/校验要求。下载到的原始 x64 Flutter JAR也与 Google Storage 的长度和内容校验值核对。

本地日志在 `.artifacts/emulator-20261024/`：`device-instrumentation.txt`、`flutter-drive.log`、`flutter-tests.log`、`analyze.log`、`build-final.log`。授权弹窗 XML/截图也保留于该目录。模拟器截图见下列链接，已查看并修正截图时的键盘过渡状态：

- [登录](previews/emulator-20261024/login-light.png)
- [首页](previews/emulator-20261024/home-light.png)
- [我的 IP](previews/emulator-20261024/resources-light.png)
- [深色设置](previews/emulator-20261024/settings-dark.png)
- [诊断](previews/emulator-20261024/diagnostics-dark.png)
- [授权拒绝](previews/emulator-20261024/vpn-permission-denied.png)
- [启动失败时保留通道](previews/emulator-20261024/vpn-start-failed-protected.png)
- [断开并释放通道](previews/emulator-20261024/vpn-disconnected.png)

没有 Git 提交、推送或生产部署；Windows/API 代码未在本轮修改。

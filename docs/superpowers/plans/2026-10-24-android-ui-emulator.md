# Android Windows 页面一致性与模拟器验收

**Goal:** 按用户要求，将 Android 前端对齐当前 Windows 界面，并通过 Android Studio 已安装的 Android Emulator 实际运行测试。

**Design authority:** 当前 `Windows/app/lib/main.dart`、`app_theme.dart`、`connection_artwork.dart` 与既有品牌资产。沿用 Windows 蓝白/深色配色、4px 圆角、品牌、页面名称与主要信息层级；手机保持可滚动布局与触控尺寸。Android 专属 VPN 功能按实际能力展示，不添加虚假 Windows 专属开关。

**Architecture:** 保留 Android 控制器、原生 VpnService 和当前 Xray 桥接。UI 与模拟器/原生验证分开实施；共享 Windows/API 源码不改，不提交混合工作区。

- [x] 对齐登录、首页、资源、设置和适用的诊断界面，官网链接固定 HTTPS `sunipip.com`；保留状态与密码保护行为。
- [x] 为 SDK 已安装的 x86_64 AVD 构建实际原生库，并保留 ARM64 支持。启动 Android Studio 工程与 AVD，不以“没有 USB 设备”跳过运行测试。
- [x] 在模拟器运行现有 Android instrumentation 测试、Flutter 界面集成测试，覆盖登录表单/导航/主题/资源和原生系统权限；界面 fixture 与真实网络证据分开标注。
- [x] 修复运行发现的具体故障；完成静态检查、必要回归、APK 打包与模拟器截图，记录命令和结果。

**Review focus:** 小屏幕与键盘溢出、旧连接身份恢复、真实 JNI 架构、官网链接、VPN 权限拒绝/允许、模拟器截图不得冒充真实节点转发结果。

结果：Flutter 102 项、实际模拟器 instrumentation 52 项、完整界面/原生授权集成流程通过。独立审阅的两项问题已修复并复核，另修复真实 API `api_v2` 身份不匹配。证据、产物哈希与验证范围见 `Android/docs/emulator-verification-20261024.md`。

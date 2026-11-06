# BlueGrass Android

Android 8.0（API 26）及以上的 Flutter 客户端，应用标识 `com.bluegrass.client`，当前安装包仅支持 ARM64（arm64-v8a）设备。

2026-11-05：**0.5.0+9** 增加真实上传/下载速度，以及总流量、总上传、总下载。按本次连接的代理业务统计，自动重连继续累计，后台停止界面轮询。统计口径与测试见[流量统计验收](docs/traffic-verification-20261105.md)。

2026-11-03：**0.4.1+8** 补齐 FlClash 风格的多层柔和阴影、连续圆角、悬浮底栏、按钮光晕及深色表面层次。详情与新版预览见[视觉修正记录](docs/visual-polish-20261103.md)。

2026-11-03：**0.4.0+7** 按 FlClash 最新正式版 v0.9.2 重做移动端前端：圆角仪表盘、悬浮导航、紧凑代理卡片及分组设置，保留连接、分流和规则自动更新功能。156 项 Flutter 测试通过，页面预览与验证边界见[本次界面验收](docs/flclash-ui-verification-20261103.md)。

2026-10-26：按手机分发需求改为 ARM64 Release 包，原生构建与 APK 均不再包含 x86/x86_64。随后启用原生库压缩，当前 APK 约 **20.5 MB**；Android 安装时解压原生库，这降低下载体积，但安装占用可能增加。详见[当前压缩包检查](docs/arm64-compression-verification-20261026.md)和[此前 ARM64 打包记录](docs/arm64-package-verification-20261026.md)。

2026-10-19：按 [ADR 0049](../docs/adr/0049-xray-only-four-platforms.md)，四端唯一代理内核改为 **Xray-core v26.10.17（上游预发布）**。Android 采用 VpnService + Go 共享原生库，保留 protect、底层 Network 绑定、系统授权、前台生命周期及暂停保护。禁止旧核心回退或第二代理内核。入口：[Android 开发手册](../docs/development/android.md)、[四端共享手册](../docs/development/four-platform-development.md)、[协议矩阵](../docs/development/xray-protocol-matrix.md)。

2026-10-24：前端已按当前 Windows 的品牌、蓝白/深色主题、卡片、登录、首页、“我的 IP”、设置与诊断布局适配手机。已重建 Xray 双架构库，并在 Android Studio 的 Android 11/API30 模拟器运行原生测试、界面与真实 VPN 授权流程，详见[模拟器验证记录](docs/emulator-verification-20261024.md)。[原 VPN 阶段记录](docs/vpn-verification.md)和[第一阶段记录](docs/verification.md)仍保留历史用途。

同日已追加正常 APK 的[真实账号与转发验收](docs/live-functional-verification-20261024.md)，在 **0.2.1+3** 修复认证 SOCKS5 节点使用独立公网 UDP 中继时被错误拒绝的问题。6 条真实资源中，3 条完整通过 DNS/TCP/UDP，1 条 TCP 正常但节点拒绝 UDP，2 条节点拒绝认证；后台、界面重建、断网自动恢复、退出与网络恢复均已实测。节点侧失败保留为失败项，不计作客户端“全部通过”。

**0.3.2+6** 增加规则自动更新：默认每天联网后台检查、启动补查，设置页可查看版本和立即更新。下载校验失败保留可用规则，正在连接的会话不切换规则，新版本在下次连接生效。验收记录见[自动更新验收](docs/rules-auto-update-verification-20261024.md)。

**0.3.1+5** 将共享规则库切换到 v2rayN 默认使用的 `Loyalsoldier/v2ray-rules-dat`，固定发布 `202610232206` 作为内置基线。来源与该版本测试见[Loyalsoldier 规则验收](docs/loyalsoldier-verification-20261024.md)。

**0.3.0+4** 在首页和设置增加“国内外分流 / 全局代理”。新安装默认国内外分流；已有安装保持全局，断开连接后可选择并保存。连接、暂停及切换资源期间维持活动会话模式。该版本测试证据见[分流模式验收](docs/routing-verification-20261024.md)。

## 构建与运行

在 Android Studio 打开本目录作为 Flutter 工程，入口 `lib/main.dart`。四端统一默认 API 为 `https://client-api.sunip.vip/api/client/v1`，官网为 `https://sunipip.com/`，远程 bootstrap 为 `https://client-api.sunip.vip/client-config/config.json`；域名变化重新登录，不转发旧环境 token。迁移后的实际接入须验证，规则见[共享配置](../client-config/README.md)。连接首次需用户接受系统 VPN 授权。

首次构建或修改 Go 源码后先生成原生库；默认只构建 ARM64：

```powershell
# Android/ 目录
.\scripts\build-native.ps1
.\scripts\check-native.ps1 -Race
```

然后在 Android/：

```powershell
flutter clean
flutter pub get --enforce-lockfile
flutter analyze
flutter test
flutter build apk --release --target lib/main.dart --target-platform android-arm64
```

[ARM64 安装包](build/app/outputs/flutter-apk/app-release.apk)使用 Release 优化构建，目前沿用原 debug 签名以保持安装签名一致，支持 Android 8.0+ 的 ARM64。正常包从 `lib/main.dart` 构建，测试入口位于 `integration_test/`。

切换 Debug/Release 时不要添加 `--no-pub`：当前 Flutter 需要按构建模式重生成插件注册文件，Release 才会排除 `integration_test` 等开发依赖。

本机 Flutter 在 `E:\tools\flutter`，SDK 在 `E:\AndroidTools\sdk`。旧终端 PATH 可按下列方式刷新：

```powershell
$env:Path = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
$env:JAVA_HOME = 'D:\Android\Android Studio\jbr'
& E:\tools\flutter\bin\flutter.bat build apk --release --target-platform android-arm64
```

[原生构建说明](native/README.md)记录锁定版本、AAR 路径、Go 测试和模块依赖。Android 工程依赖仓库内 `Windows/packages/domain`、`Windows/packages/data_source` 和 `Windows/native/core` 的公共代码，不能只复制 Android 文件夹后直接构建。

## 迁移后必须保留并复验的行为

- 连接前刷新当前资源并校验配置，只有系统授权与原生确认后更新连接状态。入口不是出口，出口检测失败保留连接，可单独重试。
- Go 对 TUN fd 使用副本，Kotlin 保留原始 fd。到期、明确撤销、配置变更或切网异常暂停转发并保留 VPN 通道；主动断开/退出清理通道。退出失败不会假装已停止。
- 临时 API 故障或 API 会话失效不凭空缩短已验证资源期限；权威资源刷新通过完整会话身份校验同步期限。切网不会延长期限。
- 全局模式的公网 IPv4 TCP/UDP 与 DNS 通过节点；国内外分流复用 Windows GeoSite/GeoIP 规则，国内域名优先直连、国外域名优先代理、未知域名按解析出的 IPv4 分类。国内域名使用受保护的 `223.5.5.5` DNS，其他公网域名经节点解析；公网 IPv6 阻断。SOCKS5 UDP 使用当前已认证协商返回的明确公网 IPv4/端口，允许节点分离入口与中继；无认证回复不能切换到其他地址，拒绝私网、域名、IPv6、零端口及认证降级。
- 普通 LAN 按已知本地路径分类；`lan`/`home.arpa` 只有匹配的本地解析器才走物理 DNS，`.local` 使用绑定物理网络的 mDNS。DHCP 搜索域不会自动扩大直连范围；自定义内网路由域配置、更多局域网拓扑仍待扩展/验证。
- 本 app 作为控制面排除在 VPN 外；原生代理套接字仍执行 protect 与底层 Network 绑定。没有公网业务直连兜底。
- 本 Alpha 不支持 always-on、重启自动恢复或进程死亡后的系统级阻断保证；不宣称已通过 Beta 的完整断线保护门禁。

账号会话通过 AndroidKeyStore AES-GCM 加密；密码及节点配置不写磁盘。Go 内核只接收节点参数，不接收账号 token。新包应按实际 Xray 和其他依赖更新许可证页，不能通过保留旧许可证文本冒充已经换核。短信登录可选，未实现时如实提示。

## 验证入口

当前包需用 ARM64 设备或 ARM64 模拟器复验；`gst_x64` 不适用于当前 ARM64 包。
以下脚本仍保留为设备验收工具，历史 x86_64 模拟器结果见上面的 2026-10-24 报告：

```powershell
# Android/：真实页面与原生 VPN 通道，受控测试账号与资源
.\scripts\test-emulator.ps1 -Device emulator-5554
```

脚本操作本次测试触发的系统 VPN 拒绝/允许弹窗，保存运行截图，并在退出时停止测试应用。详细日志位于 `.artifacts/emulator-20261024/`。

```powershell
# native/core/
go test -mod=readonly -race ./...
go vet -mod=readonly ./...

# android/；使用 Android Studio JDK
.\check-vpn-state.ps1
.\gradlew.bat :app:lintDebug :app:assembleDebugAndroidTest '-Ptarget-platform=android-arm64'
# 已连接的 ARM64 设备可运行设备测试：
.\gradlew.bat connectedDebugAndroidTest '-Ptarget-platform=android-arm64'
```

Flutter 生成的本地 `local.properties` 路径若触发 PropertyEscape，请用 Java properties 的标准转义 `E\:\\...`；该文件不进 Git。不要修改全局 SDK 布局来掩盖项目问题。

## 界面示例

以下是 Android Emulator 上实际运行的截图；账号与资源来自测试 fixture，未宣称生产节点转发：

- [登录](docs/previews/emulator-20261024/login-light.png)
- [首页](docs/previews/emulator-20261024/home-light.png)
- [我的 IP](docs/previews/emulator-20261024/resources-light.png)
- [深色设置](docs/previews/emulator-20261024/settings-dark.png)
- [诊断](docs/previews/emulator-20261024/diagnostics-dark.png)

本轮截图由 `integration_test/ui_emulator_test.dart` 与 `test_driver/emulator_driver.dart` 生成。旧 `tool/render_previews.dart` 保留为桌面渲染工具。

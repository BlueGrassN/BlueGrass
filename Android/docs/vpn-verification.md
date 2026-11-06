# Android VPN 阶段验证记录

日期：2026-10-13（Asia/Hong_Kong）。版本 **0.2.0+2 Alpha**，`com.bluegrass.client`，minSdk 26、targetSdk 36，当前开发包仅 ARM64。本轮只在本地实现、构建与测试，没有提交、推送、部署或生产账号操作。

## 本阶段交付

- Android VpnService、系统授权、前台服务与通知、连接/断开、资源切换、切网重建、系统撤权清理。
- sing-box 1.14.2 内嵌 AAR、真实 TUN fd 桥接、平台套接字保护与物理 Network 绑定。Kotlin 持有原始 fd，Go 只关闭副本。
- Flutter MethodChannel/EventChannel 连接状态、原生快照恢复、账号和请求代次隔离、实际 IPv4 出口检测。出口探测失败只影响观测，不关闭 VPN。
- 到期、明确撤销、传输配置变更或重建失败时暂停并保留 TUN；临时 API 故障保留尚在已验证期限内的会话。成功资源刷新通过完整身份匹配同步原生期限。
- 公网 IPv4 TCP/UDP 路由到所选节点；公网 DNS 走节点；公网 IPv6 拒绝。本地名称与地址仅按明确本地路径放行，应用自身作为控制面排除在 VPN 外。
- 配置只在内存传递，不放入 Intent extras 或错误正文；生产入口没有假 VPN 实现或演示账号开关。

## 已执行的验证

| 检查 | 结果 | 证明范围 |
| --- | --- | --- |
| `flutter analyze` | No issues found | Dart 静态检查 |
| `flutter test --no-pub --reporter expanded` | **94/94 通过** | 状态恢复、授权拒绝、刷新/退出、到期/撤销、旧快照隔离、平台契约和界面回归 |
| `go test -mod=readonly -race -count=1 '-tags=with_gvisor,with_quic' -json ./...` | **215 个 test/subtest pass 事件，包通过** | 真实生产编译标签下的配置、路由、DNS、租期、SOCKS、fd 与探测边界；不等于 215 个独立顶层测试 |
| `go vet -mod=readonly '-tags=with_gvisor,with_quic' ./...` | 通过 | Go 静态检查 |
| `android/check-vpn-state.ps1` | **27/27 JVM 测试通过** | 原生状态机、期限续费/缩短、配置变更、断开/暂停与清理重试 |
| `:app:lintDebug` | **0 errors，3 warnings** | 一个仅 ARM64 的 ChromeOS ABI 提示、两个冗余资源版本目录提示 |
| `:app:assembleDebugAndroidTest -Ptarget-platform=android-arm64` | BUILD SUCCESSFUL | 49 个设备测试方法编译并生成测试 APK；其中状态机 27 项另在 JVM 执行，平台 6 项与存储 16 项未在设备执行 |
| `flutter test --no-pub tool/render_previews.dart` | **1/1 通过，四张图片已查看** | 实际 Flutter 渲染，使用明确虚构的资源及连接状态，不是设备网络截图 |
| `native/build.ps1`、Flutter ARM64 debug APK | 通过 | JNI/AAR 与 APK 集成；最终二进制记录见下方 |

Go 回归使用受控内存连接/测试替身。另有实际 sing-box 实例启动测试：不创建 TUN，向本机 loopback 测试目的地拨号，在平台 socket 回调拒绝时停止，确认保护钩子被调用；没有访问真实节点或生产账号。

本地详细日志保留在 `test-vpn-final.log`、`native/core/test-results.jsonl`、`android/.native-vpn-check/` 及 Gradle 的 `build/app/reports/`。这些生成文件不作为源代码提交。

## 最终构建产物

| 文件 | 大小 | SHA-256 |
| --- | --- | --- |
| `build/app/outputs/flutter-apk/app-debug.apk` | 131,019,120 bytes（约 125 MiB，debug） | `222BFE96EF0DDC6E464B313914325E6CC4AB3E177399F30BFC4D6583F77B0BB9` |
| `android/app/libs/bluegrass.aar` | 9,944,986 bytes | `13FD0CC45D74884EC74D3E815628CC041BF9533355822743F75FBE02C8876C7A` |

从最终 APK 读取的元数据：`com.bluegrass.client`、versionName `0.2.0`、versionCode `2`、minSdk `26`、targetSdk `36`、ABI `arm64-v8a`、显示名 BlueGrass。Manifest 中服务要求 BIND_VPN_SERVICE，使用 systemExempted 前台类型，SUPPORTS_ALWAYS_ON=false，应用关闭备份与明文 HTTP。

APK 实含 `lib/arm64-v8a/libbluegrass.so` 与 Flutter 许可证资产。对 APK 内实际库执行 `go version -m` 确认 sing-box 1.14.2、gomobile 0.1.12、`with_gvisor,with_quic`、GOOS=android/GOARCH=arm64；实际 ELF LOAD 段均按 `0x4000` 对齐，`zipalign -c -P 16 4` 通过。这只验证二进制和打包对齐，没有进行 16 KiB 页设备运行验收。

`javap` 确认生成的 Bridge/Session/Platform 方法与 Kotlin 签名一致，包括 `Platform.isValid()`。构建工具链为 Flutter 3.47.7/Dart 3.13.5、Go 1.27.2、Android Studio JDK 25.0.3、NDK 28.2.13676358、Gradle 9.3.1。产物由 `native/build.ps1` 和 README 中的 Flutter 命令生成。

## 审阅与回归修复

1. 临时 API 故障和 API 会话过期曾会提前暂停仍有效资源，现按 ADR0015 保留已验证期限；成功刷新才更新配置和期限。
2. 同一资源的凭据/协议变更曾不会停止旧核心，现比较规范化传输配置并暂停；续费和缩短期限同步到原生，切网不重新计算或延长。
3. 原先依赖唤醒定时器处理期限，现 Go 转发的读写前与阻塞读取后校验 Android elapsedRealtime 和当前请求有效性。真机休眠表现仍待验收。
4. 任意 DHCP 搜索域不能扩大直连 DNS 范围；`lan`/`home.arpa` 仅用已证明在本地前缀内的解析器，`.local` 使用受平台保护并绑定网络的 UDP mDNS，不要求原始套接字能力。
5. SOCKS5 UDP 仅接受同入口明确公网 IPv4 中继及非零端口，拒绝认证降级、域名/IPv6 中继和任意其他地址。拨号上下文只控制建立阶段，成功移交后由会话取消、显式关闭或 UDP 控制通道结束负责清理；覆盖成功后的 TCP/UDP 读写及 200 次取消竞争。
6. 内核关闭失败不再永久缓存首次错误，允许重试清理；Go 设备副本与 Kotlin 原始 fd 的所有权分开。
7. 使用带协议上下文的 outbound 序列化，防止节点地址、端口和凭据静默丢失；真实引擎启动测试也确认初始网络接口发布和 DNS detour 合法性。
8. API 26 使用同值的 Linux close-on-exec 标志，避免引用 API 27 才可用的 Android 常量；没有移除文件描述符保护。

## 还未验证的设备行为

本轮 `adb devices -l` 无连接设备，因此不能声称 VPN 已在手机联网成功。下一次受控设备验收包括：

- Android 8/API26 与较新系统安装、拒绝/同意 VPN 授权、前台通知、撤权、退出及重复连接。
- JNI 回调、真实 TUN TCP/UDP、各支持协议、Android 信任库与实际出口 HTTPS 探测，核对探测失败仍保留连接。
- 抓包确认公网 DNS/IPv6 无意外直连；验证 LAN、物理 DNS、mDNS、UDP 中继和超时行为。
- Wi-Fi/移动网络切换、断网/恢复、睡眠跨期限、续费/撤销、配置变更、界面重建，检查旧请求不能覆盖新状态。
- 服务异常、厂商后台限制与进程终止。此 Alpha 明确不支持 always-on、开机自动恢复，也不保证进程死亡后的系统级阻断。

出口探测暂用与 Windows 相同的固定第三方 HTTPS 服务；第一方探测服务尚待独立部署。当前只生成 debug 包，没有发行签名、商店发布或完成对外分发许可审计。

## 界面记录

[登录](previews/vpn-login-light.png)、[连接首页](previews/vpn-home-light.png)、[资源](previews/vpn-resources-light.png)、[深色设置](previews/vpn-settings-dark.png)。图中账号、资源及连接状态全部为测试数据。

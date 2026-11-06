# Android 开发手册

本手册与[四端共享手册](four-platform-development.md)、[ADR 0049](../adr/0049-xray-only-four-platforms.md)共同执行。Android 唯一代理内核为 **Xray-core v26.10.17（上游预发布）**，固定模块、提交和校验值见共享手册；禁止捆绑 sing-box 或第二内核。

## 工程与状态

客户端入口为 [Android/README.md](../../Android/README.md)，Flutter 位于 `Android/`，Kotlin 宿主位于 `Android/android/`，Go 桥接位于 `Android/native/core/`，构建说明见[原生库手册](../../Android/native/README.md)。应用标识 `com.bluegrass.client`，现有目标 Android 8.0 / API 26 及以上、ARM64。

当前 Xray 双架构 AAR 与正常 APK 已完成模拟器验证，见 [Android 验证记录](../../Android/README.md)。原 `0.2.0 Alpha` 的测试及 APK 哈希保留历史用途。共享业务包、路由规则与原生包位于 `Windows/`，需要完整仓库布局。

## VpnService 与原生库责任

1. Kotlin 持有系统 VPN 授权、前台服务、TUN 原始 fd 和当前底层 `Network`；Go 共享库运行 Xray。Flutter 只提交已刷新的结构化节点配置和操作，不传账号 token。
2. 代理连接和受控入口 DNS 的 socket 在使用前执行 `VpnService.protect` 并绑定所选底层 Network。TCP 拨号、UDP/QUIC socket 及 DNS 都须覆盖；protect 或绑定失败必须阻止该次连接。仅调用 `setUnderlyingNetworks` 不能替代实际 socket 绑定。
3. 明确 Kotlin、Go 桥接和 Xray 各自 fd 所有权。固定上游 Android TUN 已复制传入 fd 并在关闭时结束副本读取；桥接不能重复关闭系统原始 fd，也不能泄漏自己的副本。全局 hook/环境变量与核心启停要串行管理，旧回调不能绑定到新网络或新会话。
4. 暂停与断开分离：到期、明确撤销或数据面异常保留所需阻断 VPN；主动断开释放本会话资源。关闭失败不能报告已停止；原生状态确认前不显示已连接。
5. 全局模式的公网 IPv4 TCP/UDP、DNS 通过所选资源；国内外分流复用共享 `routingpolicy` 的域名优先与 CN IPv4 分类，只有分类允许的目标使用受保护、绑定物理网络的直连。公网 IPv6 和明确拒绝目标按策略阻断。LAN 和内网名称走受控本地路径，不能把默认路由或整段 LAN DNS 豁免扩展成公网直连。
6. 网络变化时重新选择/绑定底层 Network、解析入口并重建必要连接，不延长资源期限。系统撤销 VPN、进程死亡、锁屏与重启的行为单独测试；现有 Alpha 不能宣称 always-on 或死亡后的系统级持续阻断已经通过。

共享原生层保留的 `sing-tun` 仅作 OS TUN 数据包适配，不承担远端代理协议，不是第二协议引擎；本端具体使用 Xray 内置 TUN 还是独立适配，以原生实现和 fd 验证结果记录。协议及参数范围遵循[矩阵](xray-protocol-matrix.md)，包括对 TUIC/AnyTLS 的明确拒绝。

## 开发入口

以下是新内核完成适配后要执行的检查，不是本轮已经通过的结果：

```powershell
# 仓库根目录：先重建 AAR，不能沿用旧内核产物
./Android/scripts/build-native.ps1
./Android/scripts/check-native.ps1 -Race

# Android/ 目录
flutter pub get
flutter analyze
flutter test
flutter build apk --debug --target-platform android-arm64

# Android/android/ 目录
./check-vpn-state.ps1
./gradlew.bat :app:lintDebug :app:assembleDebugAndroidTest '-Ptarget-platform=android-arm64'
```

Go 原生模块检查命令见[原生库说明](../../Android/native/README.md)。构建需要满足 Xray 的 Go 1.27 要求、兼容移动绑定工具与已安装 NDK/JDK，实际版本由新产物验证记录锁定。只有已连接并获授权的测试设备才运行 `connectedDebugAndroidTest`；编译测试 APK 不等于设备测试。

## 验证清单

- Go 配置、Xray 启停、真实受控出站与 protect 拒绝路径；Flutter、JVM 状态机、JNI 回调及错误脱敏。
- AAR 与 APK 的真实核心版本、架构、依赖和许可；debug 签名与正式签名分别记录。
- 真机 TCP/UDP/DNS、LAN/IPv6、防回环、fd 清理、切网、到期/撤销、资源切换、系统撤销、锁屏/后台与厂商策略。
- 默认 API `client-api.sunip.vip`、官网 `sunipip.com` 及共享 bootstrap 的实际集成；API 域名变化重新登录，旧 token 不跨域。

AndroidKeyStore 会话保护、密码/节点配置不落盘及账号与内核隔离继续保留。短信可选，平台真实联网与保护不能以 Windows 结果代替。

官方平台依据：[VpnService](https://developer.android.com/reference/android/net/VpnService)、[Android VPN 开发指南](https://developer.android.com/develop/connectivity/vpn)。

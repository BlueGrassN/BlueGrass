# macOS 开发手册

2026-11-06 共享架构：平台工程使用根 `packages/client`，公共业务包、Go/Xray 与契约分别位于 `packages/`、`core/`、`contracts/`；新公共功能只维护一份实现。迁移后的开发和验证要求见 [共享客户端架构](shared-client-architecture.md)。

2026-11-06 发布规则：所有可交付平台统一使用固定 [preview Release](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)，各端版本独立更新，不再自动创建平台 tag。构建/发布、来源核验和失败恢复见 [统一 GitHub 发布手册](github-releases.md)。

本手册与[四端共享手册](four-platform-development.md)、[ADR 0049](../adr/0049-xray-only-four-platforms.md)共同执行。macOS 唯一代理内核为 **Xray-core v26.10.17（上游预发布）**，固定模块与提交见共享手册；禁止 sing-box 或第二代理协议内核。

## 当前边界

2026-11-05 用户选择 Windows 开发、GitHub 构建、用户在 Mac 测试的工作方式，并明确各端 UI 必须统一。`Mac/app` 通过包依赖复用 `Windows/app` 的桌面 UI、控制器与共享业务规则，macOS 原生能力由 Swift 宿主实现。最低目标为 macOS 13，Apple Silicon 与 Intel 分别构建。

用户暂时没有 Apple Developer 账号/证书，并明确要求先通过管理员密码授权测试完整 TUN。开发包提供系统代理和 Xray 原生 utun 两种模式，使用 ad-hoc 签名，未公证。TUN 启动器仅安装并运行与构建时 SHA-256 一致的组件，认证私有 IPC 对端的 UID、PID 与进程出生时间；密码由 macOS 系统授权窗口处理。独立 root 组件负责 PF、路由、DNS 归属与恢复。详细设计见[管理员 TUN 方案](../superpowers/specs/2026-11-05-macos-admin-tun-design.md)。Windows 自动更新程序不在 Mac 运行，开发包按测试说明手动替换。

构建与验证入口见 [Mac README](../../Mac/README.md)、[测试说明](../../Mac/docs/TESTING.md)和[开发构建工作流](../../.github/workflows/macos-dev.yml)。实际 GitHub 构建和设备测试须分别记录证据。

## 开发包与后续 Packet Tunnel

当前宿主应用负责账号/API、资源、设置、主题、安全存储和诊断。管理员授权的 `BlueGrass.Tunnel` 承担全局接管，共享 Go/Xray 承担代理协议；业务 token 与数据面隔离。API HTTPS 保留原域名的证书校验，并在保护状态下使用 root 组件解析和固定的物理出口路由。暂停、进程崩溃与主动断开分别保留保护或恢复网络；恢复不能删除归属日志或全局清空 PF。

后续若采用原生 Swift Network Extension `NEPacketTunnelProvider` 分发，需要单独实现和验证以下约束，证书本身不会将当前 utun 组件转换成 Packet Tunnel：

- 通过 `NETunnelProviderManager` 与平台支持的扩展部署方式管理隧道；启动完成以平台网络设置和内核真实就绪为依据。
- 使用公开 Packet Tunnel/packetFlow 接口与经过验证的数据包桥接。Xray Darwin 自建 utun 路径需单独权限和路由验证，不自动取代产品的 Packet Tunnel 设计。
- 全局接管须明确公网 IPv4 TCP/UDP、业务 DNS、IPv6 阻断与 LAN 例外，保护状态、到期/撤销暂停与主动断开恢复分别实现。
- 核对外层连接/入口 DNS 的物理路径，网络切换和睡眠唤醒后重新建立有效绑定及路由；故障不直连、不改用第二内核。
- 若提供系统代理模式，单独实现 macOS 的代理设置归属、恢复及覆盖范围；不把 WinINET 的 `12808` 设置或 WFP 规则直接当作 macOS 实现。
- 使用 Keychain 与必要的受控应用/扩展共享空间；节点和令牌不进入普通偏好、日志或诊断。扩展退出不能依赖宿主继续存活才能清理资源。

平台 TUN/网络栈适配可以独立，但远端协议必须由 Xray 执行。协议范围见[矩阵](xray-protocol-matrix.md)，TUIC/AnyTLS 不支持；上游存在其他出站不等于客户端已经开放。

## 开发与验收步骤

1. 在 Windows 开发共享代码，在 GitHub macOS runner 构建宿主和所有原生组件。先签名 TUN 组件，再将其 SHA-256 固定进 Native 启动器；之后不得修改 TUN 组件。
2. 构建固定提交的 Go/Xray 原生库，检查平台包隔离、桥接 ABI、扩展生命周期、packet flow、socket 与停止取消；记录 Go、Xcode 和产物模块信息。
3. 实现共享 API、`https://sunipip.com/` 官网和 `https://client-api.sunip.vip/client-config/config.json` bootstrap；缓存和跨域重新登录规则按共享手册。
4. 实机测试每协议 TCP/UDP、DNS/IPv6/LAN、代理与全局接管模式边界、切网、睡眠唤醒、进程/扩展退出、到期/撤销、更新后的恢复与多用户归属。
5. 独立记录开发签名、管理员安装/取消/恢复与适用渠道的公证、升级卸载及分发验证。后续扩展签名单独验收，不能把 ad-hoc 包或其他平台测试称作正式 macOS 发行证据。

短信登录可选。开发工程、构建结果与 Mac 实机联网结果分别记录；未执行的 `flutter build macos`/`xcodebuild`、签名、公证或设备测试不得写成通过。

官方资料：[Network Extension](https://developer.apple.com/documentation/networkextension)、[NEPacketTunnelProvider](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider)、[NEPacketTunnelFlow](https://developer.apple.com/documentation/networkextension/nepackettunnelflow)。

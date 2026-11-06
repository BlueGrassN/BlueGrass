# iOS 开发手册

本手册与[四端共享手册](four-platform-development.md)、[ADR 0049](../adr/0049-xray-only-four-platforms.md)共同执行。iOS 唯一代理内核为 **Xray-core v26.10.17（上游预发布）**，固定模块与提交沿用共享手册；禁止 sing-box 回退或第二代理内核。

## 当前边界

2026-10-19 盘点时 `iOS/` 尚无应用或 Xcode 工程，本次只新增文档入口。没有可运行 iOS 客户端、XCFramework、签名构建、设备联网或商店审核证据。最低系统版本、设备架构范围和分发方式由实际工程及平台验证确定，不能从 Android/Windows 包推导。

## 原生 Packet Tunnel 要求

应用负责登录、安全会话、资源选择、远程 bootstrap 和界面。原生 Swift `NEPacketTunnelProvider` 扩展负责隧道生命周期、网络设置、DNS、路由及 packet flow；扩展中运行共享 Go/Xray 库或经过验证的原生桥接。业务账号 token 不传给 Xray，扩展只获取必要的节点与期限快照。

通过 `NETunnelProviderManager` 配置和启动 Packet Tunnel；完成系统网络设置与内核就绪后才报告成功。使用 Apple 提供的 `NEPacketTunnelFlow` 收发数据包，并通过平台适配连接 Xray。上游 Darwin fd 路径不是取得系统隧道 fd 的授权或稳定 API；不能依赖私有 KVC/未公开句柄获取方式作为正式实现基线。

实现时必须处理：

- Packet Tunnel 扩展的启动、停止、系统取消、宿主应用退出和扩展被回收；清理数据包读写、socket、Xray 实例及持有的资源，避免重复完成回调。
- 外层代理和入口 DNS 的实际网络路径，避免重新进入自身隧道；路径变化后重建必要连接。没有 Android `protect` API，不照搬 Android 绑定代码。
- TCP/UDP、业务 DNS、明确 LAN 例外、公网 IPv6 策略及暂停保护。代理出站失败不能放行公网业务直连。
- 宿主与扩展共享最小会话/配置材料时使用 Keychain 与受控 App Group，记录并限制双方权限；节点配置不写日志或诊断。
- 内存、CPU、锁屏、前后台、切网和系统回收。不能因为 Go 库编译成功就宣称满足扩展运行限制。

可用独立 OS 数据包适配器接入 Xray；不得以它为名附带另一代理协议核心。范围按[协议矩阵](xray-protocol-matrix.md)执行，TUIC/AnyTLS 明确不支持。

## 工程交付顺序与验证

1. 在具备 Xcode 的 macOS 环境建立宿主与 Packet Tunnel targets，确定 bundle IDs、Network Extension entitlement、App Group、Keychain access group 及签名配置；记录工具链与最低系统版本。
2. 为所需设备/模拟器架构生成绑定固定 Xray 的原生库，隔离 Windows 专用包，记录实际模块版本、构建标志与第三方许可。模拟器结果不能替代真机 VPN。
3. 接入共享节点 schema、明确的不支持错误、API/会话与配置版本；实现扩展生命周期、packet flow 桥接及保护，再做完整界面集成。
4. 真机验证密码登录/续期、节点刷新、每协议 TCP/UDP、DNS/IPv6/LAN、出口观测、断开恢复、到期/撤销、切网、锁屏与系统取消；记录抓包范围与资源使用。
5. 单独核验签名安装、升级和实际分发渠道。本文不承诺商店接受，也不沿用 Windows/Android 的官网安装包方式作为 iOS 分发结论。

默认地址与共享 bootstrap 必须遵循[共享手册](four-platform-development.md)。短信可选；注册、找回密码及交易交给官网。当前不提供虚构构建命令或下载包，工程建立后补入实际命令和结果。

官方资料：[NEPacketTunnelProvider](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider)、[NEPacketTunnelFlow](https://developer.apple.com/documentation/networkextension/nepackettunnelflow)、[NETunnelProviderManager](https://developer.apple.com/documentation/networkextension/netunnelprovidermanager)。

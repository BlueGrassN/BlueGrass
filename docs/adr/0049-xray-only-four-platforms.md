# ADR 0049：四个平台仅使用 Xray-core

- 状态：已接受；实现迁移与逐平台验证进行中。
- 决策日期：2026-10-19，Asia/Hong_Kong。
- 确认依据：用户明确要求 Windows、Android、iOS、macOS 全部使用 Xray，并写入四端开发手册。
- 替代：[D-02](0002-primary-client-core-scope.md) 全文，以及 D-03、D-04、D-11 中依赖旧内核的范围、配置和集成方式。

## 决定

1. 四端唯一代理协议内核为 Xray-core。Windows 使用 Go 服务/原生宿主嵌入；Android 使用 VpnService 与 Go 共享原生库；iOS、macOS 使用原生 Network Extension Packet Tunnel 与共享内核桥接。
2. 不打包 sing-box，不保留其作为回退内核，不引入第二代理协议引擎或双内核选择器。2026-10-20 用户进一步要求 Windows 整体使用 Xray：采用其原生 TUN 网卡和 TCP/UDP 栈，删除 sing-tun/gVisor 运行时。平台宿主仍负责系统权限、VPN fd、保护策略及恢复，不把这些平台职责移除；Android 本轮延期到另一会话。
3. 业务模型、平台网络接管和 Xray 配置生成保持边界。内核只接收严格校验的节点与策略字段，不接收账号 token、任意配置文本、任意脚本或外部配置文件路径。
4. 四端统一锁定官方 **v26.10.17**，提交 `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`。上游该 release 标记为 **prerelease**，不是稳定发行版。实际 Go 模块为 `github.com/xtls/xray-core v1.260327.1-0.20261017152356-92ccc939aaf4`；不存在对应的 `v1.261017.0` 模块标签。完整锁定信息见[共享手册](../development/four-platform-development.md)。
5. 当前协议范围以固定提交的[协议矩阵](../development/xray-protocol-matrix.md)为准：SOCKS5、HTTP/HTTPS、Shadowsocks、VMess、VLESS、Trojan、Hysteria2 可进入适配和验证；TUIC、AnyTLS 在该版本没有对应出站，明确拒绝连接。保留数据契约中的名称不等于提供运行支持。
6. 内核迁移不能削弱已有保护语义：Windows 保留 Go 服务、`127.0.0.1:12808` 本地代理及 WFP 保护/恢复；Android 保留 VpnService、套接字 protect、底层 Network 绑定与 fd 所有权；Apple 平台需要独立实现 Packet Tunnel 生命周期。
7. 节点有效且原生连接就绪后连接；实际 IPv4 出口在连接后观测，探测失败不拆除可用连接。不恢复已撤销的 D-07/D-29 固定预期出口门槛。资源到期、撤销及保护失败仍按各平台要求暂停。
8. 完整产品目标覆盖四端。短信登录为可选能力，不作为本轮迁移完成条件；未提供的协议、Apple 客户端、签名与设备验收不得写成已经交付。

## 版本选择与影响

2026-10-19 核对时，GitHub `releases/latest` 指向较旧的 v26.3.27；不能据此忽略官方较新的 v26.10.17 标签。采用新标签是本轮明确决定，包含较新的 TUN 实现，但预发布状态与平台风险必须保留在依赖和验证记录中。升级不能跟随浮动 `latest`，必须一起更新模块、校验值、构建信息与协议矩阵。

Xray 上游存在 TUN 源码不等于 BlueGrass 已完成路由、DNS、保护或移动系统集成；尤其不能把桌面 utun 路径当作 Apple Packet Tunnel 的验收。缺失协议返回明确不支持，不自动直连、换节点、换协议或换内核。

原 sing-box 测试、截图、AAR/APK/ZIP 哈希和验证报告保留为当时事实，不能作为新内核证据。新实现需提供依赖锁、逐模块测试、产物模块信息和对应平台的实测记录。文档更新本身不证明构建、设备、生产网络或商店分发通过。

## 依据与执行入口

- [官方 v26.10.17 release（Pre-release）](https://github.com/XTLS/Xray-core/releases/tag/v26.10.17)
- [固定提交源码](https://github.com/XTLS/Xray-core/tree/92ccc939aaf4f8965f369cf0b142e005aeaa11d5)
- [四端共享开发手册](../development/four-platform-development.md)
- [Windows](../development/windows.md) · [Android](../development/android.md) · [iOS](../development/ios.md) · [macOS](../development/macos.md)

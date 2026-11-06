# ADR 0003：多协议范围按固定 Xray 能力执行

- 状态：多协议方向已接受；旧的无条件首版全协议承诺于 2026-10-19 由 [ADR 0049](0049-xray-only-four-platforms.md) 修订。
- 原决策日期：2026-10-10；修订日期：2026-10-19，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-03。

## 历史与当前决定

用户原先选择多协议首发，范围包含 SOCKS5、SS、VMess、VLESS、Trojan、HTTP/HTTPS、Hysteria2、TUIC、AnyTLS，并以当时 sing-box 能力作为依据。2026-10-19 用户明确要求四端只使用 Xray-core，因此旧核心映射不再适用，不能以捆绑第二核心兑现原清单。

1. Windows、Android、iOS、macOS 均按[固定 Xray 协议矩阵](../development/xray-protocol-matrix.md)开发。版本为官方 v26.10.17（上游预发布），模块与提交见[共享手册](../development/four-platform-development.md)。
2. SOCKS5、HTTP/HTTPS、Shadowsocks、VMess、VLESS、Trojan、Hysteria2 进入本轮适配及测试。存在对应上游出站不等于任意传输/安全参数组合可用，也不证明任一平台已完成验收。
3. TUIC 和 AnyTLS 在固定版本没有对应代理出站，本轮明确不支持。数据 schema 可保留协议名以展示资源和明确报错；配置构建与出站创建必须拒绝，不伪装连接、不直连、不改协议或调用旧核心。
4. 每个资源仍使用服务端给定的唯一当前协议（[D-24](0024-one-protocol-per-resource.md)），同一时刻一个活动资源。协议、传输、安全与数据报能力分别建模，ConnectRequest 只接受白名单字段，不透传内核配置。
5. 各平台分别记录配置构造、受控握手、真实 TCP/UDP、DNS 与系统接管验证。HTTP CONNECT 不因资源声明支持 UDP 就具备 UDP 转发；服务端已部署范围与客户端可实现范围分别记录。

## 执行与证据

S12 继续承担协议参数、数据源契约和逐协议验证职责，基线改为固定 Xray 源码。历史报告中的旧内核测试保留，不改写为 Xray 测试。新内核的构建、参数组合、测试线路和逐平台运行结果尚待实现方提供。

本次已查固定提交的出站注册表及代理实现目录，Hysteria 配置要求版本 2；TUIC/AnyTLS 不在出站注册或代理实现目录。完整固定源码链接及当前限制集中维护在[协议矩阵](../development/xray-protocol-matrix.md)，避免各端复制并漂移。Win7 仍须独立验证，不沿用主力版兼容性结论。

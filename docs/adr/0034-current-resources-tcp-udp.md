# ADR 0034：当前全部资源支持 TCP 与 UDP

- 状态：已接受，能力契约、客户端转发及运行验收待完成。
- 决策日期：2026-10-11，Asia/Hong_Kong。
- 对应事项：C02/C05；B-02、S3/S9/S12 的资源能力与转发验收。
- 确认依据：用户选择 A，当前全部资源都支持通过代理转发 TCP 和 UDP。

## 决定

1. 当前全部资源的业务能力按 TCP、UDP 均支持设计。用户确认是业务前提，不能写成客户端或服务端已完成本轮实测。
2. TUN/VPN 中公网 IPv4 TCP 与 UDP 都通过所选资源转发，包括 UDP 上的 QUIC、语音和游戏流量；按 D-29/D-33 使用该资源同一固定 IPv4 出口。公网 IPv6 继续按 D-33 阻断，本地 TCP/UDP/IPv6 按 D-31/D-32 处理。
3. Windows 系统代理本身不接管所有应用的 UDP，不能因为资源支持 UDP 就宣称该模式已覆盖全部 UDP。需要接管应用公网 UDP 时使用全局接管模式，并保持模式说明。
4. UDP 转发故障、实际协议配置不兼容或当前网络限制时，报告具体检测/转发异常，相关业务不得本地直连兜底，也不自动换资源或协议。D-24 的每资源单协议继续适用。
5. 应用能够自行从 QUIC 改用 TCP 时，TCP 仍经同一资源；此行为不能作为 UDP 验收成功的证据，也不保证所有 UDP 应用存在 TCP 回退。

## 能力与协议契约

- 当前 Go API 没有 UDP 能力字段和完整协议参数。同步文档时保留这一事实；适配层分别记录产品声明、实际配置可表达的能力和检测结果，不把缺失字段或用户确认伪装成实测通过。
- D-03 已按 [ADR 0049](0049-xray-only-four-platforms.md) 收敛为[固定 Xray 协议矩阵](../development/xray-protocol-matrix.md)，不表示当前资源已经部署每一种协议。逐协议须核对实际 UDP 承载机制及服务端配置；配置无法承载 UDP 时记录与交付要求的差异，不能静默用另一协议或第二内核补齐。
- SOCKS5 须验证 UDP 关联、返回的中继地址/端口、认证和关联生命周期；其他协议验证各自的数据报机制。入口传输是否使用 UDP，与能否代理应用 UDP 是不同事项。
- 固定 Xray 的 [SOCKS 配置](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/socks.go)与 [HTTP 配置](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/http.go)分别接入。HTTP CONNECT 不能仅凭统一资源声明就视为具备应用 UDP 转发能力；S12 必须验证真实数据报，不能用 TCP 或外层 QUIC 连通替代。
- D-07/D-29 的固定预期出口门槛已撤销，当前连接后观测实际出口，探测失败不拆除连接。HTTPS 出口检测不能证明 UDP 可用。[D-35](0035-udp-diagnostics-only.md) 的 UDP 一键诊断不作为首次连接、切换或重连的额外门槛；UDP 转发验收仍须完成。

## 验证要求

- 当前资源分别验证 TCP 和实际 UDP 数据报的双向访问；使用受控服务核对两者观察到的出口均为权威预期 IPv4。不能用 TCP DNS、HTTPS 或网页能打开代替 UDP 证据。
- 验证域名/IP 目标、并发数据报流、空闲后重建、网络切换与重连；记录逐协议及平台的结果。配置声明支持但运行失败时，诊断显示失败原因，不修改资源的业务声明来掩盖问题。
- 覆盖 UDP 关联失败、受控目标无响应、网络限制及停止连接后的清理；TUN/VPN 中不能出现公网 UDP 本地直连，允许的 LAN、入口传输和受控 DNS 流量分别核对。
- 一键诊断覆盖 UDP；尚未执行时显示未检测，超时/失败不自动解释为资源永久不支持。实际诊断服务、协议和超时契约由 S11/S12 落实，D-28 的现有 HTTPS 检测草案不视为已经提供 UDP 探测。
- 本轮仅同步设计，没有修改 Go 代码、部署探测服务或进行网络测试。

## 关联文档

- [D-03：首版多协议范围](0003-first-release-protocol-scope.md)
- [D-24：每资源一种协议](0024-one-protocol-per-resource.md)
- [D-29：固定出口必须匹配](0029-fixed-expected-exit-ip.md)
- [D-33：当前 IPv4 出口](0033-current-ipv4-egress.md)
- [客户端 API 接入](../development/client-api-integration.md)

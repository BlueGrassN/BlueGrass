# ADR 0003：首版采用多协议方案

- 状态：已接受。首版协议清单已确认，具体版本、传输和安全参数组合由 Phase 0 验证并锁定。
- 决策日期：2026-10-10，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-03。
- 确认依据：用户先选择 B（多协议首发），随后选择清单 C（下表全部协议）。

## 背景

主设计记录当前服务交付 SOCKS5，并列出其他目标协议；审阅报告建议首版仅支持 SOCKS5。用户确认采用多协议首发，并将讨论中的清单 C 纳入首版。

## 决定

1. Windows 10/11 与安卓主力客户端首版支持下表全部协议。
2. 延续 D-02，首版使用 sing-box，通过统一资源模型和通用内核适配接口组织多协议能力。
3. 每个纳入首版的协议均需定义配置字段、资源及中转资格关联、所需认证和安全参数，并完成实际连接验收。

| 首版协议 | 对应 sing-box 出站配置 |
| --- | --- |
| SOCKS5 | `socks`，使用 SOCKS5 版本 |
| Shadowsocks（SS） | `shadowsocks` |
| VMess | `vmess` |
| VLESS | `vless` |
| Trojan | `trojan` |
| HTTP / HTTPS 代理 | `http`；HTTPS 代理启用 TLS |
| Hysteria2 | `hysteria2` |
| TUIC | `tuic` |
| AnyTLS | `anytls` |

## 实施边界

- 协议清单已定。各协议启用的版本、传输和安全参数组合，以及配置来源、测试线路和验收范围，由 Phase 0 在锁定 sing-box 版本后形成能力矩阵。
- 选定协议所需的 TLS、原始域名、SNI 和证书验证纳入首版；不自动承诺所有可选传输、插件或参数组合均已支持。
- 每种协议仍须关联同一资源及有效中转资格。协议参数不完整时明确拒绝连接，不根据 SOCKS5 字段猜测其他协议参数。
- 客户端能力目标与服务端实际部署分别记录；本决策不表示太阳IP当前已经提供所有这些协议。
- Win7 后续独立验证其内核可支持的范围，不直接沿用主力版的兼容性结论。

## 影响

- 不再以“首版仅 SOCKS5”作为范围约束。
- 补充设计中现有 SOCKS5 流量路径、认证、DNS 和探测方案保留为该协议的基线；其他协议由 Phase 0 补齐对应设计和测试矩阵。
- ConnectRequest 按协议类型校验认证、传输、安全和协议专用参数，不接收任意内核配置。
- 新增多协议验证任务 S12，扩展配置生成、契约测试和集成测试；原 354 人日与里程碑是 SOCKS5 基线，须重新估算。
- 多协议取数契约须重新核实；账号级订阅是否参与配置获取，取决于数据完整性及能否可靠关联资源和资格。

## 官方依据与验证边界

2026-10-10 核对 [sing-box 出站配置目录](https://sing-box.sagernet.org/configuration/outbound/)，以上协议均有对应的出站类型。[HTTP 出站](https://sing-box.sagernet.org/configuration/outbound/http/)提供 TLS 配置，HTTP / HTTPS 代理作为同一协议族的不同安全配置处理。实际发行版的支持范围还须结合锁定版本、构建选项和各平台实测确认。

## 关联文档

- [主设计](../superpowers/specs/2026-10-10-sunipip-client-design.md)
- [设计审阅报告](../superpowers/reviews/2026-10-10-sunipip-client-design-review.md)
- [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)
- [实施计划](../superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)
- [ADR 0002：主力客户端内核范围](0002-primary-client-core-scope.md)

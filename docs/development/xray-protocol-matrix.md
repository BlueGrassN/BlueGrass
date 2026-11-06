# 固定 Xray 版本的协议能力矩阵

核对日期：2026-10-19。依据为官方 **v26.10.17（Pre-release）**、提交 `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`、Go 模块 `v1.260327.1-0.20261017152356-92ccc939aaf4`，锁定值见[共享手册](four-platform-development.md)。已检查固定模块的 `infra/conf/xray.go`、`infra/conf/hysteria.go`、`infra/conf/http.go`、`infra/conf/socks.go` 及 `proxy/`，并收到实现方相同协议范围确认。

这是源码能力与客户端接入范围，不是四个平台的运行通过清单。本文尚未收到新内核测试、构建或设备结果。

## 产品协议与上游映射

| 资源协议 | 固定提交中的实现 | 本轮处理 | 需要的运行证据 |
| --- | --- | --- | --- |
| SOCKS5 (`socks5`) | `socks` / `proxy/socks` | 接入 Xray，严格处理认证与目标地址 | TCP、UDP ASSOCIATE、认证失败、UDP 中继和生命周期 |
| HTTP (`http`) | `http` / `proxy/http` | 接入 HTTP CONNECT | 代理认证、CONNECT 失败、域名/IP 目标；不宣称普通 CONNECT 转发 UDP |
| HTTPS 代理 (`https`) | `http` 出站加 TLS 传输 | 原始域名/SNI、证书验证必须保留 | 可信证书、错误证书、认证及握手失败；不隐式降级 HTTP |
| Shadowsocks (`shadowsocks`) | `shadowsocks`；源码另有 `shadowsocks_2022` | 仅启用连接 schema 和适配器明确接受的密码套件 | 逐套件配置、密钥校验、TCP/UDP；不推导所有 SS 变种已支持 |
| VMess (`vmess`) | `vmess` / `proxy/vmess` | 依已验证字段及传输组合接入 | 身份、加密、时间与传输组合、TCP/UDP |
| VLESS (`vless`) | `vless` / `proxy/vless` | 按适配器能力校验 TLS/REALITY、flow 与传输 | 逐组合握手、身份/证书失败与 TCP/UDP |
| Trojan (`trojan`) | `trojan` / `proxy/trojan` | 按白名单字段接入 | 密码、TLS/SNI、TCP/UDP |
| Hysteria2 (`hysteria2`) | `hysteria`，`HysteriaClientConfig.Build()` 要求 `version == 2` | 使用 Xray 的 Hysteria 2 出站与传输配置；不能直接复制旧 `hysteria2` JSON | 认证、TLS、QUIC 参数、TCP 与真实 UDP 数据报 |
| TUIC (`tuic`) | 出站注册表和 `proxy/` 中无此实现 | 本轮不支持；`Build`/`NewOutbound` 返回明确错误 | 拒绝路径与界面提示；不得调用第二内核 |
| AnyTLS (`anytls`) | 出站注册表和 `proxy/` 中无此实现 | 本轮不支持；`Build`/`NewOutbound` 返回明确错误 | 拒绝路径与界面提示；不得调用第二内核 |

保留 wire/schema 的 TUIC/AnyTLS 类型是为了正确接收、展示与诊断资源，不代表有相应运行能力。两者不得标为已接入、可连接或已验收。原 D-03 中“首版全部支持”的无条件清单由 [ADR 0049](../adr/0049-xray-only-four-platforms.md) 收敛为本表。

共享模型的 wire `Validate` 可以接受这些已知类型以保留列表和错误展示；运行能力由 `Config.Supported`、`Requirements` 以及最终 `Build`/`NewOutbound` 检查。形状校验成功不能成为启动 Xray 或启用连接按钮的能力证明，原生创建入口仍须再次拒绝不支持协议。

Xray 还包含其他出站；它们不因此进入本次 BlueGrass 产品范围。协议名、传输层、TLS/REALITY、数据报承载是不同维度，不能按名称放行所有组合。每个平台只有在包含相应实现、配置构建成功并完成该平台对应测试后，才可增加已验证声明。

## TUN 与平台边界

固定提交含 `tun` 配置及 Windows、Android、Darwin 实现，不应笼统声称 Xray 不支持 TUN。

- Windows 实现使用 Wintun；平台服务仍要负责实际路由、DNS、物理出口、WFP 和归属恢复。
- Android `NewTun` 读取应用提供的 fd 并复制，`Close` 关闭副本。应用/桥接需明确每层 fd 所有权与串行生命周期；上游文件存在不证明 Kotlin `protect` 和底层 Network 已正确绑定。
- Darwin 实现有自建 utun 与外部 fd 路径；它不等于已经建立 Apple Network Extension 工程，也不证明普通 App 可取得或使用 Packet Tunnel 内部 fd。Apple 以官方 Packet Tunnel/packetFlow 接口与独立适配为开发基线。
- 独立 OS TUN 适配器可以使用；本轮保留的 `sing-tun` 仅为 OS 数据包处理工具，远端出站仍由 Xray 执行，不得捆绑另一代理协议内核。

## 固定源码与官方资料

- [出入站注册表](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/xray.go)
- [Hysteria 配置构建](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/hysteria.go)
- [传输与安全配置](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/transport_internet.go)
- [HTTP 配置](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/http.go)与[官方 HTTP 出站说明](https://xtls.github.io/config/outbounds/http.html)
- [SOCKS 配置](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/infra/conf/socks.go)
- [完整 proxy 目录](https://github.com/XTLS/Xray-core/tree/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/proxy)
- [TUN Windows](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/proxy/tun/tun_windows.go)、[TUN Android](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/proxy/tun/tun_android.go)、[TUN Darwin](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/proxy/tun/tun_darwin.go)

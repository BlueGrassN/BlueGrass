# BlueGrass

BlueGrass 客户端及独立 Go API 的开发资料。客户端通过专用 API 接入太阳IP账号与中转节点，官网登录与客户端会话分离：

- [客户端 API 接入开发文档](docs/development/client-api-integration.md)：接口、请求响应、长期会话、并发续期、节点分页、数据库约束及客户端待办。
- [Go API 服务端](client-api/README.md)：代码、配置与运行交接；[验证记录](client-api/docs/verification.md)说明测试环境、并发结果及未验收范围。

- [设计文档（2026-10-10）](docs/superpowers/specs/2026-10-10-sunipip-client-design.md)：产品目标、模块边界、平台差异与验收要求。
- [设计审阅报告（2026-10-10）](docs/superpowers/reviews/2026-10-10-sunipip-client-design-review.md)：原审阅意见、18 项已确认决策及技术验证依赖。
- [缺失设计逐项核对（2026-10-11）](docs/superpowers/reviews/2026-10-11-bluegrass-missing-design-audit.md)：G-01～G-30 的覆盖情况、具体矛盾、剩余证据及处理顺序。
- [补充设计（2026-10-10）](docs/superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)：状态模型、资格判定、流量路径、Windows 特权服务与断线保护、Android 数据面、错误码、更新与诊断等。
- [实施计划（2026-10-10）](docs/superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)：阶段与里程碑、任务分解与估算、测试策略、质量门禁、风险登记。

独立 Go API 已在 Hytron 的 `/www/wwwroot/test-api.dos.im` 启动，使用测试库，已配置 systemd 开机自启并通过公网 HTTPS 健康检查，见[运行记录](client-api/docs/OPERATIONS.md)。客户端仍处于设计与接入准备阶段，不能把后端测试视为 Windows/Android 端到端验收。已确认的决策：

- [D-01：交付顺序与 Win7 范围](docs/adr/0001-win7-delivery-scope.md)：主力版与安卓版先发布，Win7 后续独立交付，保留系统代理、TUN 和断线保护目标。
- [D-02：主力客户端内核范围](docs/adr/0002-primary-client-core-scope.md)：首版仅集成 sing-box，保留通用内核适配接口，后续按需求增加其他内核。
- [D-03：首版协议范围](docs/adr/0003-first-release-protocol-scope.md)：SOCKS5、SS、VMess、VLESS、Trojan、HTTP/HTTPS 代理、Hysteria2、TUIC、AnyTLS。
- [D-04：入口解析与指定 DNS](docs/adr/0004-entry-dns-policy.md)：优先使用内核，原生层补齐能力；支持自定义入口 DNS、首选与备用列表及故障切换。
- [D-05：网页取数与 V2 API 预留](docs/adr/0005-web-resource-loading.md)：统一接口、按需分页及连接前刷新继续保留；网页实施路径已由 D-19 的独立 API 替代。
- [D-06：首轮 Beta 的保护范围](docs/adr/0006-first-beta-network-protection.md)：Windows 首轮 Beta 包含系统代理、TUN 和断线保护，保护与恢复验收通过后再对外发放。
- [D-07：出口验证门槛](docs/adr/0007-exit-verification-required.md)：出口检测成功后才完成连接和放行业务；失败时 TUN/VPN 保持阻断，提供重试。
- [D-08：到期后的系统代理行为](docs/adr/0008-expiry-keeps-system-proxy.md)：到期或撤销后保留代理设置、暂停业务转发，由用户选择续费、换资源或恢复网络。
- [D-09：可选崩溃上报](docs/adr/0009-opt-in-crash-reporting.md)：自建接收服务，默认关闭，用户主动开启后发送脱敏崩溃摘要；不收集使用行为。
- [D-10：自启与自动连接](docs/adr/0010-startup-and-auto-connect.md)：两项默认关闭；首次连接成功后询问一次是否开启自启；已有会话与保护恢复单独处理。
- [D-11：Windows 后台服务](docs/adr/0011-go-windows-service.md)：采用 Go 内嵌 sing-box，与安卓复用可共享的原生逻辑，平台网络接管分别实现。
- [D-12：并行资源范围](docs/adr/0012-single-active-resource.md)：首版同一时刻使用一个资源出口，可供多个程序并发访问；多 IP 与本地多端口功能后续再做。
- [D-13：分发渠道](docs/adr/0013-official-download-and-updates.md)：官网提供 Windows 安装包与 Android APK，配合应用内更新；首版不增加商店渠道。
- [D-14：SOCKS5 与加密迁移](docs/adr/0014-socks5-compatibility-and-encryption.md)：保留现有 SOCKS5 兼容，如实说明传输边界；后续按同一资源迁移加密入口，失败不自动降级。
- [D-15：缓存有效期与服务端授权](docs/adr/0015-cache-until-known-expiry.md)：取消 72 小时未复核限制，已有会话的缓存仅以已知有效期为上限；最终授权与撤销由服务端执行。
- [D-16：登录方式](docs/adr/0016-password-and-sms-login.md)：保留密码与短信登录产品目标；D-19 改为独立客户端 API，当前仅密码登录已实现，短信契约仍待补齐。
- [D-17：关闭窗口](docs/adr/0017-close-to-tray.md)：Windows 点击“×”默认隐藏到托盘，首次提示一次；托盘“退出”执行完整停止与恢复。
- [D-18：品牌与发行标识](docs/adr/0018-bluegrass-branding.md)：应用名称 BlueGrass，Android 包标识 `com.bluegrass.client`；图标按 BlueGrass 品牌方向设计。
- [D-19：独立客户端 API 与长期会话](docs/adr/0019-independent-client-api.md)：Go 服务、短期访问令牌与滑动续期、独立会话、只读业务查询及有界数据库并发。

D-01 至 D-19 已记录。下一阶段完成独立 API 的客户端适配及测试环境联调，继续验证协议配置、原生内核与网络保护。短信登录、完整资产列表、其他协议和出口验证契约仍有缺口；实际图标素材、客户端工程与运行验收尚未完成。原 354 人日与日期是审阅基线，须按最终范围与验证结果重新估算。

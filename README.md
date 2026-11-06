# BlueGrass

- [Windows 自动构建与下载](Windows/docs/github-release.md)：本地测试打包、main 推送后 GitHub Actions 编译并发布到 [Releases](https://github.com/BlueGrassN/BlueGrass/releases)。

BlueGrass 客户端及独立 Go API 的开发资料。客户端通过专用 API 接入太阳IP账号与中转节点，官网登录与客户端会话分离：

Android 构建入口：本地执行 `./Android/scripts/build-release.ps1`；推送 `main` 后，GitHub Actions 自动生成 ARM64 APK 并发布到 [Releases](https://github.com/BlueGrassN/BlueGrass/releases)。本仓库已配置固定 Alpha 签名，仓库结构与操作说明见 [Android 自动构建与发布](Android/docs/github-release.md)。

2026-10-19：Windows、Android、iOS、macOS **全部仅使用 Xray-core**，禁止旧核心回退或第二代理内核。当前固定官方 v26.10.17（上游预发布），实际 Go 伪版本及逐平台状态见共享手册。Windows/Android 正在迁移；iOS/macOS 尚无可运行客户端。旧内核测试不能作为本次验证。

- [四端共享开发手册](docs/development/four-platform-development.md)：统一内核、依赖锁、API/远程配置、连接保护和验证证据。
- 平台手册：[Windows](docs/development/windows.md) · [Android](docs/development/android.md) · [iOS](docs/development/ios.md) · [macOS](docs/development/macos.md)。
- 平台工程入口：[Windows](Windows/README.md) · [Android](Android/README.md) · [iOS](iOS/README.md) · [Mac](Mac/README.md)。
- [原始品牌图片](assets/branding/)：集中保存各尺寸 PNG 与 SVG；平台生成资源及校验清单见 [docs/branding/](docs/branding/)。
- [D-49：四端仅使用 Xray-core](docs/adr/0049-xray-only-four-platforms.md)与[固定版本协议矩阵](docs/development/xray-protocol-matrix.md)：TUIC/AnyTLS 不支持并明确拒绝，其余协议的实际验收按平台记录。

- [客户端 API 接入开发文档](docs/development/client-api-integration.md)：接口、请求响应、长期会话、并发续期、节点分页、数据库约束及客户端待办。
- [客户端 API 部署手册](client-api/docs/DEPLOYMENT.md)：`127.0.0.1:3306` 同库单账号 `v2_test` 配置、[新建 3 张表的 SQL](client-api/deploy/01-create-client-tables.sql)、宝塔反代、服务启动、验证、升级与回滚。
- [四端共享配置与 API 域名切换](client-config/README.md)：独立 `client-config/` 目录、公开配置部署、远程切换、递增序号、客户端重启和回退操作。
- [Go API 服务端](client-api/README.md)：代码、配置与运行交接；[验证记录](client-api/docs/verification.md)说明测试环境、并发结果及未验收范围。
- [全项目复核（2026-10-11，Go API 落地后）](docs/superpowers/reviews/2026-10-11-bluegrass-project-reaudit.md)：后端新增缺口、客户端剩余契约，以及旧 F01～F14 的当前状态；本轮为源码与文档审计。

- [设计文档（2026-10-10）](docs/superpowers/specs/2026-10-10-sunipip-client-design.md)：产品目标、模块边界、平台差异与验收要求。
- [设计审阅报告（2026-10-10）](docs/superpowers/reviews/2026-10-10-sunipip-client-design-review.md)：原审阅意见、18 项已确认决策及技术验证依赖。
- [缺失设计逐项核对（2026-10-11）](docs/superpowers/reviews/2026-10-11-bluegrass-missing-design-audit.md)：G-01～G-30 的覆盖情况、具体矛盾、剩余证据及处理顺序。
- [补充设计（2026-10-10）](docs/superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)：状态模型、资格判定、流量路径、Windows 特权服务与断线保护、Android 数据面、错误码、更新与诊断等。
- [开发实施计划（2026-10-11 修订）](docs/superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)：冻结本轮决定，按契约、工作包、依赖和发布门禁安排开发；旧固定日期及工期不再作为执行承诺。
- [开发启动任务清单](docs/superpowers/plans/2026-10-11-bluegrass-development-kickoff.md)：K-01～K-09 的目标文件、接口、行为测试、检查命令与完成条件，从开发环境和共享契约开始。

全部 Windows 客户端代码和测试位于 [Windows/](Windows/README.md)，默认生产 API 已于 2026-10-15 切换为 `client-api.sunip.vip`，并接入四端共享远程配置；见 [API 迁移验证](Windows/docs/api-migration-20261015.md)。按用户 2026-10-12 的修正，有效节点直接连接，随后检测并显示实际出口 IP；检测失败保留连接，不再要求预期出口或测试绑定。连接行为与实测见 [连接与出口检测验证](Windows/docs/observed-exit-verification.md)，完整发行差距见 [Beta 清单](Windows/docs/beta-completion.md)。下列 ADR 中 D-07/D-29 的旧出口门槛已被本次修正替代：

- [D-01：交付顺序与 Win7 范围](docs/adr/0001-win7-delivery-scope.md)：主力版与安卓版先发布，Win7 后续独立交付，保留系统代理、TUN 和断线保护目标。
- [D-02：主力客户端内核范围](docs/adr/0002-primary-client-core-scope.md)：历史决定，已由 D-49 全文替代；四端唯一代理内核为 Xray-core。
- [D-03：首版协议范围](docs/adr/0003-first-release-protocol-scope.md)：按固定 Xray 能力接入 SOCKS5、SS、VMess、VLESS、Trojan、HTTP/HTTPS、Hysteria2；TUIC/AnyTLS 不支持。
- [D-04：入口解析与指定 DNS](docs/adr/0004-entry-dns-policy.md)：优先使用内核，原生层补齐能力；支持自定义入口 DNS、首选与备用列表及故障切换。
- [D-05：网页取数与 V2 API 预留](docs/adr/0005-web-resource-loading.md)：统一接口、按需分页及连接前刷新继续保留；网页实施路径已由 D-19 的独立 API 替代。
- [D-06：首轮 Beta 的保护范围](docs/adr/0006-first-beta-network-protection.md)：Windows 首轮 Beta 包含系统代理、TUN 和断线保护，保护与恢复验收通过后再对外发放。
- [D-07：旧出口验证门槛](docs/adr/0007-exit-verification-required.md)：已由用户撤销；当前为连接后检测实际出口，失败不阻断连接。
- [D-08：到期后的系统代理行为](docs/adr/0008-expiry-keeps-system-proxy.md)：到期或撤销后保留代理设置、暂停业务转发，由用户选择续费、换资源或恢复网络。
- [D-09：可选崩溃上报](docs/adr/0009-opt-in-crash-reporting.md)：自建接收服务，默认关闭，用户主动开启后发送脱敏崩溃摘要；不收集使用行为。
- [D-10：自启与自动连接](docs/adr/0010-startup-and-auto-connect.md)：两项默认关闭；首次连接成功后询问一次是否开启自启；已有会话与保护恢复单独处理。
- [D-11：Windows 后台服务](docs/adr/0011-go-windows-service.md)：采用 Go 内嵌 Xray-core，复用共享原生逻辑，平台服务、TUN/VPN 与保护分别实现。
- [D-12：并行资源范围](docs/adr/0012-single-active-resource.md)：首版同一时刻使用一个资源出口，可供多个程序并发访问；多 IP 与本地多端口功能后续再做。
- [D-13：分发渠道](docs/adr/0013-official-download-and-updates.md)：官网提供 Windows 安装包与 Android APK，配合应用内更新；首版不增加商店渠道。
- [D-14：SOCKS5 与加密迁移](docs/adr/0014-socks5-compatibility-and-encryption.md)：保留现有 SOCKS5 兼容，如实说明传输边界；后续按同一资源迁移加密入口，失败不自动降级。
- [D-15：缓存有效期与服务端授权](docs/adr/0015-cache-until-known-expiry.md)：取消 72 小时未复核限制，已有会话的缓存仅以已知有效期为上限；最终授权与撤销由服务端执行。
- [D-16：登录方式](docs/adr/0016-password-and-sms-login.md)：独立 API 密码登录与长期会话；短信作为可选能力，未实现时如实提示，不作为本轮迁移门槛。
- [D-17：关闭窗口](docs/adr/0017-close-to-tray.md)：Windows 点击“×”默认隐藏到托盘，首次提示一次；托盘“退出”执行完整停止与恢复。
- [D-18：品牌与发行标识](docs/adr/0018-bluegrass-branding.md)：应用名称 BlueGrass，Android 包标识 `com.bluegrass.client`；图标按 BlueGrass 品牌方向设计。
- [D-19：独立客户端 API 与长期会话](docs/adr/0019-independent-client-api.md)：Go 服务、短期访问令牌与滑动续期、独立会话、只读业务查询及有界数据库并发。
- [D-23：资源选择默认可连接视图](docs/adr/0023-default-connectable-resource-view.md)：默认展示可连接资源，切换“全部资源”查看其他已购资源；没有可连接资源不等于没有购买。
- [D-24：每个资源一种协议](docs/adr/0024-one-protocol-per-resource.md)：服务端为每个资源提供一种协议，列表每个资源一条，详情展示协议信息；不同资源可以使用不同协议。
- [D-25：仅展示当前归属资源](docs/adr/0025-currently-owned-resources-only.md)：“全部资源”包含到期未回收的本人资源，已回收或转出归属的移除，不保留历史条目。
- [D-26：沿用现有短信服务](docs/adr/0026-reuse-existing-sms-provider.md)：复用官网现有供应商、签名和模板，由独立 Go API 发码、校验并签发客户端会话；短信接口仍待实现。
- [D-27：短信限流、不使用人机验证](docs/adr/0027-sms-limits-without-captcha.md)：不接入图形或滑块，采用服务端发送频率、发送数量和验证码错误次数限制；具体参数待确认。
- [D-28：独立出口检测服务](docs/adr/0028-independent-exit-probe-service.md)：自建出口检测独立于业务 API 部署，不依赖业务数据库，保留备用端点；当前为连接后观测实际出口，探测失败不拆除可用连接。
- [D-29：旧固定预期出口匹配规则](docs/adr/0029-fixed-expected-exit-ip.md)：已撤销。客户端不要求 API 提供预期出口，也不比较预期值；显示连接后的实际观测。
- [D-30：官网处理订阅，客户端读取状态](docs/adr/0030-website-commerce-client-readonly.md)：购买和续费留在官网，客户端读取资源、连接配置及到期时间，不设计交易流程或后台续订规则。
- [D-31：默认允许 LAN，按本地路由处理](docs/adr/0031-default-local-network-routing.md)：RFC 私有地址、ULA、链路本地等按用途走原有本地路径，补充共享地址兼容；FakeIP 和业务 DNS 优先按专用规则处理，平台行为待验证。
- [D-32：内网域名使用本地解析](docs/adr/0032-local-domain-dns.md)：明确的内网域名走相应本地 DNS，`.local` 使用 mDNS；公网域名继续走代理 DNS，两类解析失败不互相回退。
- [D-33：当前统一为固定 IPv4 出口](docs/adr/0033-current-ipv4-egress.md)：预期字段与出口探测采用 IPv4，TUN/VPN 中公网 IPv6 保持接管并阻断；允许的本地 IPv6 单独按本地规则处理。
- [D-34：当前全部资源支持 TCP 和 UDP](docs/adr/0034-current-resources-tcp-udp.md)：全局接管中两类公网 IPv4 业务均走所选资源的固定出口；逐协议验证实际转发，故障不直连兜底，系统代理覆盖范围单独说明。
- [D-35：UDP 检测放入一键诊断](docs/adr/0035-udp-diagnostics-only.md)：连接、切换和重连不额外等待 UDP 探测，固定预期出口门槛已撤销；UDP 检测状态单独展示，转发验收仍须完成。
- [D-36：普通更新后台下载](docs/adr/0036-background-update-download.md)：自动下载并校验，完成后由用户确认安装；下载不触发维护暂停或重启，网络条件按 D-37 执行。
- [D-37：计费网络下载须经同意](docs/adr/0037-metered-update-download.md)：手机流量或计费网络上暂停自动下载，用户同意本次下载后继续；下载途中切网也检查条件，非计费网络恢复后可续传。
- [D-38：到期时间显示北京时间](docs/adr/0038-beijing-expiry-display.md)：统一显示 UTC+8 并标注时区，内部仍以 UTC 判断到期；设备时区改变不影响展示，空期限显示“未指定到期”。
- [D-39：首次预选可连接资源](docs/adr/0039-first-resource-preselection.md)：首次登录无历史选择时预选首条可连接资源，用户点击后才连接；不覆盖已有选择或会话，不冒充成功连接历史。
- [D-40：最近成功连接优先](docs/adr/0040-recently-connected-first.md)：列表默认按本机最近成功连接顺序优先展示，其余保持接口顺序；失败和预选不更新记录，排序不改变分页游标或连接选择。
- [D-41：首页网络统计](docs/adr/0041-home-traffic-statistics.md)：显示上传/下载速度及本次业务流量；同会话重连继续累计，新会话清零，仅本地展示，不作为官网计费数据。
- [D-42：浅色与深色主题](docs/adr/0042-light-dark-system-theme.md)：默认跟随系统，可手动选择浅色或深色并保存；切换主题不影响连接、选择或统计。
- [D-43：首页完整显示出口 IP](docs/adr/0043-full-home-exit-ip.md)：默认展示完整地址，区分预期、实际及上次验证结果，旧连接结果不冒充当前已验证出口。
- [D-44：普通更新采用应用内提示](docs/adr/0044-nonmodal-update-ready-notice.md)：下载校验完成后显示横幅或角标，不弹安装提醒或抢焦点，用户主动选择安装。
- [D-45：Windows 默认系统代理](docs/adr/0045-windows-default-system-proxy.md)：首次采用系统代理，需要时手动开启全局接管并保存选择；实际模式以原生状态为准，默认值不覆盖已有会话或保护。
- [D-46：四档到期提醒](docs/adr/0046-expiry-reminder-schedule.md)：到期前 7 天、3 天、1 天及到期时各提醒一次；按资源和到期时刻去重，时间更新后重排，不影响原生到期暂停。
- [D-47：全部归属资源到期提醒](docs/adr/0047-expiry-reminders-all-owned-resources.md)：覆盖近期未使用、不可连接及后续分页中的当前资源；后台分批同步无凭据的到期摘要，现有节点接口不足以完成此范围。
- [D-48：合并多资源到期提醒](docs/adr/0048-grouped-expiry-notifications.md)：同轮多资源合并一条摘要，点击查看各资源信息；按资源独立去重，分页补充不反复新增通知。

D-01 至 D-19、D-23～D-48 及 [D-49](docs/adr/0049-xray-only-four-platforms.md) 已记录；旧内核决定按 D-49 替代。开发按新版总计划和 K-01～K-09 启动清单推进；短信具体参数继续延后，余额为后期功能，购买和续费仍由官网处理。当前可开展基础工程、契约及模拟界面；真实连接、完整资产/提醒、短信和发布分别等待相应接口与平台证据，不以文档完成或旧工期代替验收。

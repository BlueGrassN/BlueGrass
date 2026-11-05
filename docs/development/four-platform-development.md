# BlueGrass 四端共享开发手册

更新日期：2026-10-20。本文是 Windows、Android、iOS、macOS 的共同开发约束，平台细节分别见 [Windows](windows.md)、[Android](android.md)、[iOS](ios.md)、[macOS](macos.md)。内核决策见 [ADR 0049](../adr/0049-xray-only-four-platforms.md)；本文与该 ADR 替代旧文档中的 sing-box 主内核和双内核扩展安排。

## 当前状态与交付范围

| 平台 | 已有工程基础 | Xray 迁移状态 | 尚需证据 |
| --- | --- | --- | --- |
| Windows 10/11 x64 | Flutter、Go 原生宿主/服务、系统代理、WFP/TUN 基础 | 已完成 Windows +17 原生 TUN/协议栈迁移；Go race/vet、Flutter、驱动与便携包校验通过；4 条真实线路通过、1 条上游认证拒绝 | [原生 TUN 迁移证据](../../Windows/docs/xray-native-tun-verification.md)；签名安装与提权 TUN/WFP 实机验收仍未完成 |
| Android 8.0+ / API 26、ARM64 | Flutter、Kotlin VpnService、Go AAR 桥接 | 实现方正在迁移；旧 Alpha 报告不能证明新内核可用 | 新 AAR/APK 构建、JNI/protect/绑定、真机 TCP/UDP/DNS 与生命周期 |
| iOS | 此次盘点尚无客户端工程；目录仅建立文档入口 | 开发要求已记录，尚无可运行客户端 | Xcode 工程、Packet Tunnel、原生库、签名、设备与分发验证 |
| macOS 13+ / Apple Silicon、Intel | 2026-11-05 新建共享 Flutter UI 的 Mac 工程、Swift 宿主与 Go 系统代理适配 | Windows 开发、GitHub 构建开发测试包；TUN 在无开发者证书的本轮明确禁用 | GitHub 编译记录与用户 Mac 实测分开记录；签名 Network Extension、公证及正式分发待完成 |

完整产品要求包括真实账号会话、当前归属资源、配置刷新、可靠连接/暂停/恢复、实际出口与统计、诊断、平台保护、安装及更新。短信登录可选，接口不可用时如实提示；不能以其尚未实现冒充其他平台已经完成，也不因此增加本轮迁移门槛。Win7 仍按 [D-01](../adr/0001-win7-delivery-scope.md) 独立兼容性计划处理，不从 Windows 10/11 构建推导可用性。

## 唯一内核与依赖锁

| 项 | 本轮固定值 |
| --- | --- |
| 上游 | [XTLS/Xray-core](https://github.com/XTLS/Xray-core) |
| Release | [v26.10.17](https://github.com/XTLS/Xray-core/releases/tag/v26.10.17)，上游 `prerelease=true` |
| Commit | `92ccc939aaf4f8965f369cf0b142e005aeaa11d5` |
| Go module | `github.com/xtls/xray-core` |
| Module version | `v1.260327.1-0.20261017152356-92ccc939aaf4` |
| Module sum | `h1:FlGykoNEWRZN5ExwxSEU5lnuEWU5CD3SJFyKKWLChl8=` |
| 上游 Go 要求 | 固定提交的 `go.mod` 为 `go 1.27`；实际构建工具链及移动绑定工具版本另记于产物证据 |

官方 release 标签与 Go 模块版本并不一一对应，不能把本次标签推导为不存在的 `v1.261017.0`。`releases/latest` 当时仍指向 v26.3.27；本轮明确选择新官方预发布标签，不能写成“最新稳定版”。各平台必须使用相同提交和模块校验值，不能一个平台继续旧核心、另一个平台浮动到 main。

禁止捆绑 sing-box、隐式降级内核或另一个代理协议引擎。按 2026-10-20 用户进一步要求，Windows 的 TUN 网卡和 TCP/UDP 协议栈也统一为 Xray 原生实现。其他平台沿用唯一 Xray 的方向，由各自宿主处理系统 VPN 权限、fd 和路由，并独立验证；不能照搬 Windows 网卡创建或旧内核 JSON。

Windows 已改用 `github.com/xtls/xray-core/proxy/tun`，不再编入 sing-tun/gVisor，也不需要 `with_gvisor` 标签。保留的 sing 通用地址/缓冲区和 bbolt 缓存工具不是代理或 TUN 内核。Android 的独立模块本轮未修改或验收，交给另一会话继续。依赖审计以各平台实际可达模块和二进制为准。

共享配置编译、Xray 生命周期和协议能力集中维护；平台宿主负责 socket 物理网络绑定、权限、设备与保护。Windows 与 Android 目前复用 `Windows/native/core/` 中跨平台包，Android 还依赖 `Windows/packages/domain/` 和 `Windows/packages/data_source/`，开发时保持仓库布局。Apple 原生桥接需要隔离 Windows 系统包，不能仅复制 Windows 二进制。

本会话在 2026-10-19 按用户最新要求仅完成 Windows 迁移与交付。Android 已有的中间改动交由另一会话继续，不在本次构建或验收范围；iOS/macOS 仍按各自手册标注的工程状态推进。四端唯一 Xray 的长期要求不变。

## 控制面与资源契约

| 用途 | 默认地址/契约 |
| --- | --- |
| 客户端 API | `https://client-api.sunip.vip/api/client/v1` |
| 官网、注册、找回密码、购买/续费 | `https://sunipip.com/` |
| 四端共享远程 bootstrap | `https://client-api.sunip.vip/client-config/config.json` |

远程配置格式、缓存、递增序号和回退按[共享配置说明](../../client-config/README.md)实现。域名变化在下次启动按契约生效；跨域不复用旧环境登录 token，重新登录。上述地址是四端统一开发要求，不能据此宣称尚未构建的平台已经完成接入。

使用 [API 接入文档](client-api-integration.md)的会话、游标分页、详情刷新和错误契约。一个资源使用服务端给定的一种当前协议，同一时刻一个活动资源；不新增协议选择器。未知协议或不支持组合应给出明确错误，不修改返回值伪装可连接。TUIC/AnyTLS 名称可保留在传输数据模型供展示与兼容，但 `Build`/`NewOutbound` 必须拒绝，参见[协议矩阵](xray-protocol-matrix.md)。

账号令牌留在业务层和各平台安全存储；原生内核只接受必要节点认证、期限和策略，配置留在内存。日志/诊断不保存账号密码、token、节点密码、订阅 URL 或完整代理链接。真实凭据不得进入示例和产物。

## 连接、DNS 与保护

1. 连接前刷新资源，验证身份、期限、协议及参数；原生连接就绪后报告状态，随后经所选节点观测实际 IPv4 出口。入口地址不能冒充出口；未观测显示未知，探测失败不拆除已建立连接，不要求 `expected_exit_ip`。
2. 暂停和主动断开分别处理：到期/明确撤销/数据面失败暂停业务并保留该模式要求的保护；用户主动断开按归属恢复系统网络。旧会话迟到回调不能影响新资源或解除其保护。
3. 入口 DNS 走受控物理网络；公网业务 DNS 经所选资源；明确内网域名按本地 DNS/mDNS 路径。公网 DNS 失败不回落本地，入口解析失败不借业务直连绕过。
4. 目标仍为公网 IPv4 TCP/UDP 经所选资源，公网 IPv6 按当前产品政策阻断，本地网络按明确规则处理。HTTP CONNECT 不自动提供应用 UDP；TCP 连通、QUIC 建连或 HTTPS 出口检测都不能代替 UDP 数据报测试。
5. Windows 系统代理只覆盖遵循代理设置的流量；TUN/VPN 才承担相应全局接管。任何模式都不得宣称其尚未验证的持续断线保护、进程死亡恢复或零泄漏。
6. 切网后更新物理网络、入口解析、socket 绑定和相关路由/DNS；不能只重启 Xray 就声称所有平台恢复正确。不得因代理故障自动直连、换资源、换协议或加载第二内核。

2026-10-20 Windows 实测补充：SOCKS5 认证后的 UDP ASSOCIATE 可返回不同于 TCP 入口的公网中继地址，不得仅因异址而拒绝。平台实现必须核对该关联的原始认证和精确 BND 响应，再按会话授权具体地址/端口，继续拒绝私网及虚拟地址等不安全目标。本次仅实现和验证 Windows，其他平台按各自手册验收。Windows 原生 Xray TUN 的 DNS 对端地址要求和真实测试结果见[管理员验收](../../Windows/docs/elevated-tun-acceptance.md)。

## 验证与证据记录

每次实现方回传结果须带日期、代码状态/修订、平台和架构、Go/移动工具链、命令、退出码、产物路径与哈希、Xray 实际模块版本。状态区分“未执行”“失败”“本地通过”“设备通过”，不能只有总数或一句完成。

| 层级 | 必须证明的事项 | 不能代替 |
| --- | --- | --- |
| 依赖与配置 | 模块锁一致；无旧代理核心；参数白名单；不支持协议拒绝；TLS/SNI 不丢失 | 远端握手 |
| 单元及受控集成 | 核心启停、原生回调、真实 Xray 本地出站、取消/重连、错误与统计归属 | 真实 TUN 与设备 |
| 构建与打包 | 正确架构；AAR/APK/ZIP/原生库含固定核心；许可清单；无旧核心载荷 | 安装、签名及商店认可 |
| 系统/设备 | 每协议实际 TCP/UDP、DNS、IPv6、LAN、切网、暂停、断开/崩溃恢复 | 其他平台的相同能力 |
| 发行 | 安装卸载、签名/更新、保护恢复、平台分发要求 | 内部未签名开发包 |

四端各自更新手册状态；未收到证据前保留“待验证”。旧 [Windows 验证](../../Windows/docs/verification.md)、[Android VPN 验证](../../Android/docs/vpn-verification.md)及历史计划不改写过去的测试结果。迁移后的复测单独记录，不能把旧内核名称替换成 Xray 后沿用测试结论。

## 许可与资料

固定 Xray 提交的[许可文件](https://github.com/XTLS/Xray-core/blob/92ccc939aaf4f8965f369cf0b142e005aeaa11d5/LICENSE)为 MPL-2.0。它不自动改变 BlueGrass 自身许可或其他依赖许可；新发行物应按实际依赖提供第三方声明和对应源码/构建资料。旧 GPL/sing-box 声明仅用于含该组件的历史包，不能据此把新包描述成仍内嵌旧核心，也不能只删除许可文本就宣称依赖已清除。

品牌素材以仓库根目录当前素材及资产实施方输出为准；本文不记录未经验证的图标替换或打包结果。

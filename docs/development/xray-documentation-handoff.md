# Xray 四端文档迁移交接

日期：2026-10-19。本文记录本次文档分工及只读实现审计，应用代码、素材、构建脚本和主实施计划不在本分工写入范围。未提交或推送 Git。

## 结果与替代关系

四端唯一核心统一为官方 Xray v26.10.17，明确保留上游预发布状态、实际 Go 伪版本、固定提交及模块校验值。`sing-tun` 仅为 OS 数据包适配工具，不能作为旧代理核心回退。协议矩阵区分上游源码能力、客户端适配和逐平台验收；TUIC/AnyTLS 明确不支持，wire schema 保留不代表可连接。

- D-49 全文替代 D-02；修订 D-03 的无条件全协议承诺与 D-11 的内嵌核心，继续保留 Go Windows 服务、12808 端口及 WFP 保护语义。
- D-04、D-31、D-34 的技术依据改为固定 Xray 源码与平台适配；Android 明确 VpnService、protect、Network 绑定及 fd 所有权。
- D-16 明确短信可选；实际出口按连接后观测处理，不恢复旧预期出口门槛。
- 主设计、补充设计、总计划及 README 同步；旧审阅、启动计划和阶段报告只增加失效范围说明，不改写过去的测试数量、版本和结论。
- iOS/macOS 新增开发入口和原生 Packet Tunnel 要求，明确暂无工程、可运行客户端或平台验收。

## 本分工文件清单

| 文件 | 操作 |
| --- | --- |
| [根 README](../../README.md) | 更新四端入口和当前决定 |
| [Windows README](../../Windows/README.md) | 更新内核要求与历史证据边界 |
| [Android README](../../Android/README.md) | 更新内核、API 默认值和验证入口 |
| [Android 原生 README](../../Android/native/README.md) | 重写 Xray 桥接、构建和所有权要求 |
| [Mac README](../../Mac/README.md) | 新增目录开发入口 |
| [iOS README](../../iOS/README.md) | 新增目录开发入口 |
| [ADR 0002](../adr/0002-primary-client-core-scope.md) | 标为全文已替代，保留历史 |
| [ADR 0003](../adr/0003-first-release-protocol-scope.md) | 收敛为固定 Xray 协议范围 |
| [ADR 0004](../adr/0004-entry-dns-policy.md) | 更新 DNS 技术依据 |
| [ADR 0011](../adr/0011-go-windows-service.md) | Go 服务保留，内嵌核心改为 Xray |
| [ADR 0016](../adr/0016-password-and-sms-login.md) | 短信改为可选范围 |
| [ADR 0031](../adr/0031-default-local-network-routing.md) | 更新 LAN/DNS 路由技术依据 |
| [ADR 0034](../adr/0034-current-resources-tcp-udp.md) | 更新 UDP 能力及旧出口门槛说明 |
| [ADR 0049](../adr/0049-xray-only-four-platforms.md) | 新增已接受的四端唯一核心决定 |
| [四端共享手册](four-platform-development.md) | 新增共同约束、版本锁及验证状态 |
| [Xray 协议矩阵](xray-protocol-matrix.md) | 新增固定源码证据与能力边界 |
| [Windows 手册](windows.md) | 新增服务、12808、系统代理/TUN/WFP 开发要求 |
| [Android 手册](android.md) | 新增 VpnService/原生库开发要求 |
| [iOS 手册](ios.md) | 新增 Packet Tunnel 开发要求 |
| [macOS 手册](macos.md) | 新增应用/Packet Tunnel 开发要求 |
| [Windows 早期进度](windows-implementation-progress.md) | 仅增加历史说明 |
| [主设计](../superpowers/specs/2026-10-10-sunipip-client-design.md) | 更新当前核心、协议、平台和许可引用 |
| [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md) | 更新进程图、原生边界、DNS 与短信范围 |
| [总实施计划](../superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md) | 保留任务编号，更新当前依赖与范围 |
| [历史开发启动计划](../superpowers/plans/2026-10-11-bluegrass-development-kickoff.md) | 仅增加历史说明 |
| [历史 Android foundation 计划](../superpowers/plans/2026-10-13-android-foundation.md) | 仅增加历史说明 |
| [历史 Android VPN 计划](../superpowers/plans/2026-10-13-android-vpn.md) | 仅增加历史说明，保留旧测试事实 |
| [历史设计审阅](../superpowers/reviews/2026-10-10-sunipip-client-design-review.md) | 仅增加历史说明 |
| [历史缺失设计审计](../superpowers/reviews/2026-10-11-bluegrass-missing-design-audit.md) | 仅增加历史说明 |
| [历史全项目复核](../superpowers/reviews/2026-10-11-bluegrass-project-reaudit.md) | 仅增加历史说明 |
| [本交接文件](xray-documentation-handoff.md) | 新增清单、证据需求与只读审计快照 |

主实施方拥有的 `docs/superpowers/plans/2026-10-19-xray-migration.md` 未由本分工修改。品牌实施方的 `docs/branding/` 素材、manifest 和验证文件也未由本分工修改。

## 并行实现中的只读审计快照

以下为文档交付时的中间扫描结果，实施方可能正在改写这些文件；应在最终集成后复核，不把中间快照当成最终代码审查结论。

| 位置 | 观察与后续动作 |
| --- | --- |
| `Windows/native/core/protocolconfig/native.go:8` 起 | 仍导入 sing-box adapter/log 与各协议构造器，`Register` 包含 TUIC/AnyTLS；需要完成 Xray 实例/出站替换及不支持错误检查 |
| `Windows/native/core/go.mod:8`、`Android/native/core/go.mod:61` | 扫描时仍分别保留 sing-box 直接/间接依赖；源码迁移结束后 tidy 并核对依赖可达图和最终二进制 |
| `Android/native/core/dependencies.txt:45` | 仍记录旧 AAR 的 sing-box 依赖；须由新 AAR 的真实模块信息重新生成，不能手工替换版本字符串冒充构建 |
| `Android/native/build.ps1:27` | 旧入口仍使用旧 feature tags 且无新核心依赖检查；建议委托至新的 `Android/scripts/build-native.ps1`，或明确退休。文档已改用新的 build/check-native 入口 |
| `Windows/native/core/README.md:3` | 活动说明仍宣称内嵌 sing-box 1.14.2；由原生实施方同步，本分工无写入权限 |

`Windows/app/lib/crash_reports.dart:462` 兼容旧 `sing-box/版本` 报告格式属于数据解析，不能据此认定旧核心仍在运行；实现方应决定是否保留历史队列兼容。打包检查中明确拒绝 sing-box 的字符串是必要断言，不应删除。`sing-tun`、`sing`、构建工具以及依赖版本中包含 `sing-box-mod` 的名称也不能仅按文字命中判断为第二代理核心。

## 尚需实现方回传的证据

1. 最终共享核心代码与两个 Go 模块的依赖锁、测试/静态检查输出；真实 Xray 受控出站以及 TUIC/AnyTLS 拒绝路径。
2. Windows 新 ZIP/安装包及 Android 新 AAR/APK 的产物哈希、实际内核模块信息、许可载荷和构建工具链；依赖检查不能只看源码声明。
3. Windows 12808/系统代理恢复，以及提权服务/TUN/WFP 的实机网络和异常恢复结果；Android protect/绑定、JNI/fd、真机 TCP/UDP/DNS/IPv6/LAN、切网与系统生命周期结果。
4. iOS/macOS 工程、原生桥接、Packet Tunnel、签名及设备证据；尚未实现时保持对应状态，不从其他平台推导通过。

本分工验证仅涉及文档本地链接、固定源码引用、活动规则残留与空白检查；未运行应用构建、真实线路或设备验收。新运行证据到达前，各手册继续标记迁移/验证待完成。

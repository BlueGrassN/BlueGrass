# Android 分流模式验收

日期：2026-10-24，Asia/Hong_Kong。正常入口 `lib/main.dart`，版本 **0.3.0+4**，Android Studio 的 `gst_x64` 模拟器（Android 11/API30/x86_64）。本页记录国内外分流；此前资源可用性见[真实功能验收](live-functional-verification-20261024.md)。

## 用户行为与实现

首页和设置均提供“国内外分流 / 全局代理”。新安装默认国内外分流，已有设备没有该偏好时迁移为全局；偏好写入成功才改变选择。连接、暂停、保存期间禁止切换；重新连接或切换资源保留活动会话的实际模式，显式断开后才能更改。

`global` 将公网 IPv4 TCP/UDP 和 DNS 交给节点。`bypass_cn` 复用 Windows 的 `routingpolicy`：国内域名优先直连、国外域名优先代理，未知域名与字面 IPv4 按 CN IP 规则分类；国内域名使用 `223.5.5.5`，其余公网解析经节点。规则固定为 `geosite-20261021011254_geoip-202610050438_v1`，校验通过才加载，没有运行时下载或第二代理内核。

FakeIP 保留域名与候选地址关系，避免共享 IP 造成域名策略串用。验证后的解析结果按 A/CNAME 最小 TTL 缓存，最长 300 秒；零 TTL/负结果不缓存，并发查询共享工作。TCP 在可观察的建链失败后尝试其他合规候选，DNS 多地址保留应用自身重试能力。成功刷新会唤醒等待该解析的连接。暂停、退出和网络失效仍受会话保护；公网 IPv6 阻断，已识别局域网维持原规则，节点出口检测始终走节点。

## 自动与设备测试

- Flutter：116 项通过，analyze 无问题，包含保存失败、保存期间禁止连接、旧快照、活动模式恢复与重连、初始化失败后重试、窄屏和大字。
- Android 设备：70 项 instrumentation 通过，包含新安装/已有安装偏好迁移、存储失败、模式身份、状态恢复与真实 JNI 校验；最终 APK 的 4 项 JNI 测试另外复验通过。
- Go：85 个顶层测试、240 个子测试通过（共 325 个通过事件），race 无告警，vet 通过。包含真实 Xray HTTP 握手首次 502、应用选择第二个 DNS 地址后 200 并传输数据，以及成功 DNS 刷新唤醒已有等待者。
- 独立复核发现并修复了重连误用保存模式、初始化恢复绕过迁移、重复 DNS 与多候选地址处理等问题。Flutter/Kotlin 复核和最终原生复核均无剩余可操作问题；设备流量证据单独记录。

## 真实转发方法

使用仓库已有指定测试账号的正常会话与已验证资源，不把账号或节点凭据写入测试包。使用 **UID 2000** 的独立 shell 探针，流量纳入 VPN；应用自己的原生出口观测只用作节点出口对照。

物理基线在 VPN 断开时采集两份：`10.0.2.3:53` 为普通网络 DNS，`223.5.5.5:53` 为分流国内域名使用的 DNS。TLS 证书验证保持开启。

| 流量 | 目标与验证方法 |
| --- | --- |
| 国外 TCP | HTTPS `api.ipify.org`，备用 `ipv4.icanhazip.com`；观测出口匹配所选节点 |
| 国外 UDP | Cloudflare、Google STUN；映射出口匹配节点 |
| 国内 TCP 域名 | HTTPS `myip.ipip.net`，保留域名、SNI 与证书校验；与相同解析条件的基线对照 |
| 国内 TCP 字面 IP | 固定 `117.149.203.79:443`，仍校验 `myip.ipip.net` TLS；直接证明同一目标在两模式的出口变化 |
| 国内 UDP 域名 | `stun.miwifi.com:3478`；通过应用 DNS/FakeIP 路径观测映射出口 |
| 国内 UDP 字面 IP | 固定 `111.206.174.2:3478`；比较同一目标的映射出口 |

上述国内域名与字面 IP 均已用实际打包的共享规则分类。`myip.ipip.net` 在普通 DNS 与国内 DNS 下解析到不同服务入口，国内 DNS 的检测响应地址与通用出口站不同；VPN 断开时同样复现。因此域名 HTTPS 与**相同 DNS 的直连基线**比较，不能将该站单个响应视为所有目的地唯一的真实源地址。字面 IP HTTPS 与国内域名/IP STUN 另外验证实际直连。备用 `4.ipw.cn` 在物理基线就无法解析，保留失败记录，不作为本次必过目标。

最终安装包的两模式验收脚本退出码 **0**，所有必过断言为真：

| 必测项 | 全局代理 | 国内外分流 |
| --- | --- | --- |
| 公网 DNS | 通过 | 通过 |
| 国外 HTTPS、两个国外 STUN | 节点出口，全部通过 | 节点出口，全部通过 |
| 国内域名 HTTPS | 节点出口，通过 | 与相同 DNS 的直连响应一致，通过 |
| 国内字面 IP HTTPS | 节点出口，通过 | 物理网络出口，通过 |
| 国内域名 / 字面 IP UDP | 节点出口，均通过 | 物理网络出口，均通过 |
| 活动模式显示与禁止切换 | 通过 | 通过 |

已在最终运行中确认：应用 UID10133 排除于 VPN，独立探针 UID2000 在 VPN 规则覆盖范围。普通网络与节点出口不同，具备区分条件。设置持久化经过实际强制关闭/重启验证，升级前已有设备初始保持全局；新安装默认值由真实 Android 存储测试验证。

最终回归第一次访问国内固定 IP HTTPS 出现一次 `https_failed`，保留在 `global-first-attempt.json` 与 `first-attempt-summary.json`。没有改动应用实现，相同安装包、相同目标随后复测通过；这次瞬时连接失败没有被删除或计作首次通过。国内 DNS 条件差异则通过单独直连基线明确区分。

测试结束时首页选中“国内外分流”，VPN 已断开，TUN 与前台 VPN 服务均不存在，指定测试账号会话保留。设备 AndroidRuntime 日志无本应用崩溃记录，临时网络探针已从模拟器移除。

## 最终构建

- APK：`build/app/outputs/flutter-apk/app-debug.apk`，**224,803,677 bytes**，版本 **0.3.0+4**，ARM64/x86_64，minSdk26、targetSdk36，debug 签名。
- APK SHA-256：`FBA14397C2BC4F9A07C0571DA01CC52117FDF56BD4284E31E86A51227A78EB87`。
- AAR：`android/app/libs/bluegrass.aar`，**25,616,457 bytes**，SHA-256 `44225BE78787B1AD2BCD29F0E0ACD074667B18D5BCCCA722A3A45562621CCB3B`。
- 两个实际 ELF 分别为 ARM64 33,789,744 bytes、x86_64 36,057,680 bytes；Xray 固定 `v1.260327.1-0.20261017152356-92ccc939aaf4`，未链接 sing-box，依赖清单从最终 AAR 提取。APK 的 16KB zip alignment 检查通过，已安装在模拟器。

## 复现与证据

结果位于 `.artifacts/routing-20261024/`，含 `baseline.json`、`domestic-baseline.json`、`targets.jsonl`、`global.json`、`bypass_cn.json`、`routing-summary.json`、Flutter/Go/Android 测试日志和构建清单。

`scripts/build-network-probe.ps1` 构建不含账号的独立 ELF；探针与参数见[使用说明](../tools/network-probe/README.md)。推送探针并建立上述两种物理基线后，正常 APK 登录到指定测试账号，在首页预选第 1 条可用资源、保持 VPN 空闲，运行：

```powershell
# Android/，设备 emulator-5554
.\scripts\test-live-routing.ps1
```

脚本通过实际页面切换与连接，检查活动模式禁用状态、DNS/HTTPS/STUN 出口，失败保留结果并断开 VPN。`live-routing-ui.ps1` 提供单步页面操作。仅把两模式的真实结果作为分流功能验收，不外推为所有生产资源、其他协议或手机厂商后台策略均已通过。

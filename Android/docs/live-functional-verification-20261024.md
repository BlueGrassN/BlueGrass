# Android 真实功能验收

日期：2026-10-24，Asia/Hong_Kong。使用 Android Studio 已有 `gst_x64` AVD（Android 11/API30、x86_64、WHPX）、设备 `emulator-5554`，测试正常入口 `lib/main.dart` 的 **0.2.1+3** APK。测试账号来自仓库已有私有文件，仅运行时读入内存并经真实登录表单输入；没有把账号、密码或节点配置注入 APK，也没有更改密码或生产资源。

## 已发现并修复的客户端故障

修复前，在真实节点 1、2 上 DNS 与 HTTPS 正常，而 UDP STUN 超时。独立 SOCKS5 握手核查发现：节点认证成功，返回有效公网 IPv4 UDP 中继，但该中继不同于入口地址。Android 错误地要求两者相同，拒绝了有效中继；当前 Windows 已支持这种部署。

`native/core/socks.go` 现允许当前完整 RFC1929 认证会话返回不同的明确公网 IPv4 中继，并使用返回的实际 IP/端口。仍通过 protect、底层 Network 绑定及会话生命周期管理套接字；无认证不能换地址，私网、环回、域名、IPv6、零端口与认证降级仍被拒绝。没有公网直连兜底或跨流复用中继。

新增回归先复现 `UDP_RELAY_DENIED`，修复后通过。完整原生 `go test -race` 为 **213 个测试/子测试通过、0 失败**，`go vet` 通过；独立代码复核没有剩余生产代码问题。双 ABI AAR 重建后，节点 1、2 的真实 UDP 流量恢复。

## 真实账号和全部资源

实际登录成功，加载 6 条可连接资源；全部资源中订阅 6 条、资产 0 条，无请求错误。安装升级与正常进程重启均恢复加密保存的登录会话。以下编号对应本次资源列表原始顺序，不输出账号或节点身份。

测试通过真实页面逐条选择、连接/切换，随后运行 **独立 shell UID 2000** 的网络探针。BlueGrass UID 10133 被排除在自身 VPN 外，而系统 VPN 规则覆盖 UID 0–10132、10134–99999，因此探针流量确实进入 TUN；应用自身的出口观测另行比对。

| 资源序号 | DNS | TLS/HTTPS TCP | UDP STUN | 应用/TCP/UDP 出口 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 1 | 通过 | 通过 | 两个目标通过 | 全部一致 | 本轮完整通过 |
| 2 | 通过 | 通过 | 两个目标通过 | 全部一致 | 本轮完整通过 |
| 3 | 通过 | 通过 | 两个目标均失败，重试一致 | 应用与 TCP 一致 | 节点拒绝 UDP_ASSOCIATE，回复码 `0x02` |
| 4 | 通过 | 通过 | 两个目标通过 | 全部一致 | 本轮完整通过 |
| 5 | 失败 | 无法完成 | 无法完成 | 无可用出口 | API 下发凭据被节点拒绝认证 |
| 6 | 失败 | 无法完成 | 无法完成 | 无可用出口 | API 下发凭据被节点拒绝认证 |

节点 3、5、6 的分类由 Windows 宿主独立标准 SOCKS5 握手复核，绕开 Android 实现，且均重复验证两个 STUN 目标的建链阶段：3 完成认证后拒绝 UDP 请求，即使诊断请求显式携带目标也返回相同 `0x02`；5/6 在认证阶段拒绝。没有修改这些生产节点或其凭据。完整资源测试返回失败状态并保留失败项，不能将结果表述为“6 条全部可用”。界面的“已连接”仅证明本地 VPN 通道建立，不替代这些转发结果。

公网 DNS 查询使用 `example.com`，VPN DNS 为 `172.19.0.2:53`。HTTPS 访问 `api.ipify.org`，备用 `ipv4.icanhazip.com`，保持 TLS 证书验证。UDP 使用 Cloudflare `3478` 和 Google `19302` STUN，并核对返回的映射公网 IPv4。最初未连接基线中 Cloudflare UDP 不通、Google UDP 通过，故每条资源至少要求一个 STUN 目标成功；本次完整通过的三条资源均两个成功。最后退出后的物理网络基线两个 STUN 均通过，说明最初 Cloudflare 失败存在时变的外部路径因素。

## 生命周期

选用已经完整验证的第 1 条资源，13 个生命周期断言全部通过，脚本最终退出码 0：

| 操作 | 实际验证结果 |
| --- | --- |
| 按 HOME 进入后台 | TUN 保留，独立 UID 的 DNS、HTTPS、两个 STUN 通过，出口仍为选中节点 |
| 重建 Activity | `NEW_TASK | CLEAR_TASK` 只替换本应用 Activity；前后 ActivityRecord 不同、进程 PID 相同、TUN 保留；新页面恢复已连接状态，真实 DNS/TCP/UDP 仍通过 |
| 刷新资源、开始诊断 | 状态保持已连接，TUN 保留 |
| 关闭模拟器 Wi-Fi 与移动数据 | 自动进入暂停，TUN 保留；探针无成功 DNS/TCP/UDP 转发 |
| 恢复模拟器网络 | 无需再点击连接，自动恢复已连接；DNS/TCP/两个 UDP 目标再次通过，出口不变 |
| 已连接时退出账号 | 返回登录页，TUN 消失 |
| 退出后强制停止并重新启动 | 仍显示登录页，会话没有残留恢复 |
| 退出后的普通网络 | DNS `10.0.2.3:53`、HTTPS、两个 STUN 通过，TCP/UDP 恢复到相同物理出口且不同于节点出口 |

最初尝试的 `always_finish_activities` 设置没有实际销毁 Activity，该次断言失败，没有计作通过；恢复该设置后改用上述可核验的 Activity 替换方式。最终模拟器 Wi-Fi、移动数据恢复原值，应用停留登录页，没有遗留 VPN 通道。

## 构建与复现

- APK：`build/app/outputs/flutter-apk/app-debug.apk`，**187,089,992 bytes**，版本 **0.2.1+3**，debug 签名，minSdk26、targetSdk36，ARM64/x86_64。
- APK SHA-256：`A8E3ADEEC1375E5E888D93AA9CA70579EAB8E8E56470413235ADCC1A45752DB1`。`zipalign -c -P 16 4` 通过，已在模拟器安装并实际运行。
- AAR：`android/app/libs/bluegrass.aar`，**25,033,211 bytes**，SHA-256 `6D6107F45133576DCE606EA98E758F1C5EE06E37319EC33CA600FACD547788FD`。
- AAR 两个 ABI 均核对实际 ELF 构建信息，固定 Xray `v1.260327.1-0.20261017152356-92ccc939aaf4`，没有链接 sing-box。`native/core/dependencies.txt` 已从该实际 AAR 更新。
- 原始脱敏结果位于 `.artifacts/live-functional-20261024/`：`resources-summary.json`、`node-*-fixed.json`、`node-03-fixed-retry.json`、`node-03/05/06-host-udp.json`、`lifecycle-summary.json`、`background-probe.json`、`activity-recreated-probe.json`、`network-offline-probe.json`、`network-recovered-probe.json`、`final-baseline-probe.json`、`native-tests.jsonl`。出口 IP 仅保存在本地探针结果，报告不包含节点入口、用户名、密码或令牌。

在本次指定模拟器与账号环境中：

```powershell
# Android/；正常 APK、首页、第 1 条预选资源、VPN 空闲且已经登录
.\scripts\build-network-probe.ps1
& E:\AndroidTools\sdk\platform-tools\adb.exe -s emulator-5554 push .artifacts/live-functional-20261024/network-probe /data/local/tmp/bluegrass-network-probe
& E:\AndroidTools\sdk\platform-tools\adb.exe -s emulator-5554 shell chmod 755 /data/local/tmp/bluegrass-network-probe
.\scripts\test-live-resources.ps1
# 包含已知节点侧失败时返回非零，仍测完其余资源并断开 VPN。

# 重新启动正常应用，使首页预选第 1 条资源，保持已登录且 VPN 空闲
.\scripts\test-live-lifecycle.ps1
```

`scripts/live-login.ps1` 仅从指定私有文件读取账号并输入真实表单；`scripts/check-live-socks-udp.py` 是仅运行时读取该账号的宿主核查工具，最后撤销自身创建的 API 会话。不要将凭据作为命令参数，也不要在登录填表期间截图。

本次真实资源均为 SOCKS5，结果不外推为 VLESS/VMess/Trojan/Shadowsocks 等其他协议的线上验收，也不覆盖手机厂商后台策略、吞吐性能或发布签名。此前 102 项 Flutter 与 52 项模拟器 instrumentation 及界面/授权 fixture 测试见[前一轮报告](emulator-verification-20261024.md)，它们和本轮真实流量证据分别记录。

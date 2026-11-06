# Android Loyalsoldier 规则验收（2026-10-24）

本轮将 Android/Windows 共用的 `routingpolicy` 数据源切换为 v2rayN 默认使用的
`Loyalsoldier/v2ray-rules-dat`，Android 版本为 **0.3.1+5**。最终 APK 已在 Android Studio
模拟器完成两模式真实网络验收；历史版本结果保留在[0.3.0 分流验收](routing-verification-20261024.md)。

## 规则来源

[v2rayN 的 GeoUrl](https://github.com/2dust/v2rayN/blob/master/v2rayN/ServiceLib/Global.cs)
默认指向 Loyalsoldier 的 `geosite.dat` / `geoip.dat`。本次固定
[发布 202610232206](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202610232206)，
标签提交为 `b8ee45341f4995e9bcfd5babe667412841a43ffa`。

| 原始文件 | SHA-256 |
| --- | --- |
| geosite.dat | `9cb5ab39ce601ad17d4077909b4d4e943c212af7baa658a612adc64e1c201ca4` |
| geoip.dat | `a6271d3c96361d96156e6d3f7cf1550ef21b00d2f951deb772f464db91e89ac5` |

只提取 `CN`、`GEOLOCATION-!CN` 域名与 CN IPv4，保留上游匹配类型，确定性去重、排序、
编码，随应用内置。来源、输出哈希和许可见共享包的 `assets/metadata.json` 与
`assets/NOTICE.md`。本轮替换规则库，不增加 v2rayN 的广告/自定义分流模板或在线自动更新。

原始文件均已下载并校验上述 SHA-256；生成后 `-check -offline` 逐字节一致。
内置数据为 110,243 条 CN 域名、27,463 条 GEOLOCATION-!CN 域名、6,522 条 CN IPv4 前缀。
域名文件 2,496,765 bytes，SHA-256
`6e0213910bbee514dd2934c10017064ac44e6d42110ccd80ac0e121f586a2c12`；
IPv4 文件 65,228 bytes，SHA-256
`9f2c27d565691f334ede744e267ebbe5cb9ad6a81f5d779552017d189b8e10ef`。

“国内外分流 / 全局代理”选择、国外域名优先代理、国内域名直连、未知域名按 IP 分类的
行为保持既有契约。DNS、FakeIP、连接生命周期和账户逻辑没有改动。

## 本轮自动检查

| 检查 | 结果 |
| --- | --- |
| Flutter test / analyze | 116 项通过；无静态问题 |
| 共享 routingpolicy | 16 个顶层测试、74 个通过事件；race/vet 通过 |
| Android 原生 bridge | 85 个顶层测试、325 个通过事件；race/vet 通过 |
| Windows localengine / tunengine 调用方 | 33 / 130 个顶层测试通过，共 59 / 309 个通过事件 |
| 离线重生成 | 数据、元数据、生成代码与许可逐字节一致 |
| 独立源码复核 | 本轮来源、许可、生成器、回归与报告目录改动无待修正问题 |
| 最终 APK 设备 JNI | 4 项通过，验证 Xray 加载与两种路由模式配置 |

新数据回归在旧包上先失败，换入新数据后通过：`119.29.29.29` 归入 CN，字面访问可直连；
国外域名解析到该 IP 仍走代理。全部上游许可文本已按固定提交和哈希校验。
这里的 Go 事件数包含子测试，与顶层测试数区分统计。

## 设备验收方法

使用 Android Studio 的 `gst_x64` 模拟器（Android 11/API 30/x86_64），安装正常
`lib/main.dart` APK，沿用指定测试账号和可用资源。独立探针使用 UID 2000，避免应用
自身被 VPN 排除导致错误结论。真实 HTTPS 保持 TLS 证书校验，真实 UDP 使用 STUN。

断开 VPN 时分别采集 `10.0.2.3:53` 和 `223.5.5.5:53` 两套直连基线。国内域名
HTTPS 与相同 DNS 的基线对照；固定 IP HTTPS、国内域名/IP STUN 另行证明实际转发路径。
`4.ipw.cn` 在物理基线无法解析，作为可选目标保留失败记录。

本轮原始结果位于 `.artifacts/loyalsoldier-20261024/`。复现两模式验收：

```powershell
# Android/，准备好独立探针、两份基线与空闲且已登录的正常 APK
.\scripts\test-live-routing.ps1 -ReportDirectory E:/workspace/APP/Android/.artifacts/loyalsoldier-20261024
```

## 真实功能结果

最终正常 APK 的验收脚本完成，退出码 0；两种模式所有必过断言均为真。

| 必测流量 / 行为 | 全局代理 | 国内外分流 |
| --- | --- | --- |
| 公网 DNS | 通过 | 通过 |
| 国外 HTTPS | 所选节点出口，通过 | 所选节点出口，通过 |
| 国外 Cloudflare / Google STUN | 两个目标均走节点，通过 | 两个目标均走节点，通过 |
| 国内域名 HTTPS（myip.ipip.net） | 节点出口，通过 | 与相同国内 DNS 的直连基线一致，通过 |
| 国内固定 IPv4 HTTPS（117.149.203.79） | 节点出口，通过 | 物理网络出口，通过 |
| 国内域名 UDP（stun.miwifi.com） | 节点出口，通过 | 物理网络出口，通过 |
| 国内固定 IPv4 UDP（111.206.174.2） | 节点出口，通过 | 物理网络出口，通过 |
| 活动模式正确显示、连接时禁止切换 | 通过 | 通过 |

国内 DNS 下域名 HTTPS 的响应地址与通用出口站不同，但 VPN 断开时使用相同 DNS
可复现；国内固定 IP HTTPS 和域名/IP STUN 均独立匹配物理出口。节点出口与物理出口不同。
实际规则分类记录在 `targets.jsonl`；所有必测国内目标均匹配新 CN 规则。

设备 VPN 覆盖 UID `0–10132, 10134–99999`：探针 UID2000 在范围内，应用 UID10133
在范围外，避免用应用自身排除流量代替 VPN 转发验收。原始结果为 `global.json`、
`bypass_cn.json`、`routing-summary.json` 和 `vpn-uid-coverage.txt`。

升级与强制关闭/重启后均保留“国内外分流”。重启检查首次点击早于界面就绪；等待同一
进程首页出现后验证通过，记录在 `restart-ui-first-attempt.txt` 与 `restart-state.json`。
测试结束时 VPN 空闲，无客户端 TUN/前台 VPN 服务，临时探针已删除，未见本应用崩溃记录。
模拟器 shell 的 `ip link` 访问被拒绝，最终网络接口检查改用成功返回的 `/sys/class/net`，
结果见 `final-state.json`。本轮实测使用已有 SOCKS5 资源，覆盖上述 HTTPS/UDP 目标。

## 最终构建

- APK：`build/app/outputs/flutter-apk/app-debug.apk`，0.3.1+5，192,727,156 bytes，
  SHA-256 `f1319817956ad695c780805721a4f9e4c80c437dc9216f1a09aef8d81503ccad`。
- AAR：`android/app/libs/bluegrass.aar`，26,765,051 bytes，
  SHA-256 `282766c1a2b8a88bcabf41b339aa5a4b90294de66bfbba7eb111f0e9c8af9c22`。
- APK 含 ARM64 / x86_64，minSdk26、targetSdk36，debug 签名；16KB zip alignment 通过。
- 两种 ABI 的 AAR 与 APK 原生库均逐字节检出两份新规则数据；依赖清单从最终 AAR
  的实际 ELF 提取，Xray 固定 `v1.260327.1-0.20261017152356-92ccc939aaf4`，无 sing-box。
- Android 包已安装到 `emulator-5554`；Windows 共享源码与调用方测试已更新，
  本轮未重建 Windows 安装包。

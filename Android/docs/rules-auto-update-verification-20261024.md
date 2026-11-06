# Android 规则自动更新验收（2026-10-24）

版本 **0.3.2+6**。规则来源仍为 v2rayN 默认的 `Loyalsoldier/v2ray-rules-dat`；
内置 `202610232206` 用作离线基线，更新不再依赖重装 APK。

## 实现行为

- Android JobScheduler 持久化每日任务，联网可执行；启动/回到前台补查。系统可延后任务。
- 成功检查间隔 24 小时；失败自动重试间隔至少 1 小时，系统采用指数退避。设置页“立即更新”可跳过检查间隔。
- 读取官方 release 元数据，下载该发布的两个文件；限制大小、HTTPS 主机与重定向，核验 SHA-256、protobuf、分类和匹配器，拒绝版本倒退。
- 两份数据作为一个缓存包原子提交，保留上一份有效缓存，损坏时退回可用缓存或内置规则。进程终止遗留的自有临时文件在启动时清理，与写入互斥。
- 已有 VPN 会话保留其不可变规则和 DNS/FakeIP 映射，新规则在下次连接生效；更新不会主动断开 VPN。
- VPN 连接时通过所选节点及其公网 DNS 下载；未连接时使用应用普通网络。会话结束取消该会话承载的更新，错误不会丢弃可用规则。
- 设置页显示版本、检查/更新时间、更新状态和固定错误提示。状态读取不触发网络请求。

调度依据 [Android JobInfo 文档](https://developer.android.com/reference/android/app/job/JobInfo.Builder)，
发布及校验信息依据 [GitHub Releases API](https://docs.github.com/en/rest/releases/releases#get-the-latest-release)。
Windows 继续使用内置加载入口；本轮后台调度和交付面向 Android。

## 自动和设备测试

| 范围 | 本轮结果 |
| --- | --- |
| Flutter | 151 项通过，analyze 无问题 |
| Android instrumentation | 89 项通过，含 19 项新调度/取消/通道/JNI 用例 |
| 最终 APK JNI | 原生库和更新状态契约 5 项再次通过 |
| Android Go bridge | 88 个顶层测试，328 个通过事件，race/vet 通过 |
| 共享规则匹配/原始数据导入 | 21 个顶层测试，119 个通过事件，race/vet 通过 |
| 更新与缓存 | 最终 8 个顶层测试、17 个通过事件，race/vet 通过 |

新增回归覆盖坏哈希、截断/非法/重复/过大数据、版本倒退、失败退避、并发检查和取消、
磁盘提交失败、缓存损坏和中断恢复、自有临时文件清理，以及“旧会话不变、新会话使用新规则”。
已用真实官方两个原始文件导入并与内置目标分类对照，通过。

设备测试核实实际 JobScheduler 注册的每日间隔、网络约束、持久化、小时退避、服务权限，
并验证重复进入前台不会重置任务周期。无效输入/异常仅以固定代码跨通道传递。
独立复核指出中断后的临时文件残留，已修复并复核通过。

一次宽泛 `go test ./...` 与 gomobile 构建重叠，误扫 Android 临时生成包而发生宿主编译失败；
实际 bridge/共享包均通过。随后明确选择实际包完成 race/vet，保留原始日志
`native-tests-build-overlap.jsonl`，未将该失败记为通过。

## 正常 APK 官方更新

通过系统 `cmd jobscheduler run -f` 提前执行应用实际注册的前台补查任务（ID46865），
由 JobService 调用正常 `UpdateRoutingRules(false)`，未替换下载端点或伪造规则。
这验证实际后台路径；每日周期由设备注册测试核对，没有宣称等待了自然的 24 小时。

初次所选资源的公网 DNS/HTTPS/UDP 均超时，国内直连正常。重连后仍失败，换到已有
第 2 条资源后全部恢复，失败记录保留。为在同次验收中重试后台路径，仅清除了更新器的
失败检查时间状态并重启应用，再选中已验证资源；账号与分流偏好未清除。
资源列表刷新曾有一次网络超时，重试成功；重启后资源默认选择首项，因此验收显式重新选择可用项。

后台更新成功写入 `202610232206`，状态 `source=downloaded`、`lastError` 为空。
从应用私有目录读取实际 7,915,848-byte 缓存包并独立解包，两个原始文件均与官方发布一致：

| 文件 | 字节 | SHA-256 |
| --- | ---: | --- |
| geosite.dat | 11,384,991 | `9cb5ab39ce601ad17d4077909b4d4e943c212af7baa658a612adc64e1c201ca4` |
| geoip.dat | 16,570,425 | `a6271d3c96361d96156e6d3f7cf1550ef21b00d2f951deb772f464db91e89ac5` |

下载过程中独立 UID2000 探针的公网 DNS、HTTPS 和两个 STUN 通过。
设置页实际显示“已下载规则”；点击“立即更新”后检查时间推进，版本与更新时间保持不变，
没有重下同版本数据。自动和手动更新后 VPN 都保持连接。
证据包括 `automatic-update-result.json`、`download-verification.json`、
`healthy-during-auto-update.json`、`rules-ui-downloaded.json`、`manual-update-result.json`。

## 下载规则后的实际转发

断开已有会话后使用下载快照建立新连接，两个模式所有必过断言均通过。
独立探针 UID2000 在 VPN 覆盖范围 `0–10132, 10134–99999` 内，应用 UID10133 排除，
避免将应用自身的排除流量误作 VPN 转发证据。

| 流量 / 行为 | 全局代理 | 国内外分流 |
| --- | --- | --- |
| 公网 DNS | 通过 | 通过 |
| 国外 HTTPS | 节点出口，通过 | 节点出口，通过 |
| Cloudflare / Google STUN | 两个目标均通过 | 两个目标均通过 |
| 国内域名 HTTPS | 节点出口，通过 | 匹配相同 DNS 直连基线，通过 |
| 国内固定 IP HTTPS | 节点出口，通过 | 物理出口，通过 |
| 国内域名 / 固定 IP UDP | 节点出口，均通过 | 物理出口，均通过 |
| 活动模式显示与禁止切换 | 通过 | 通过 |

国内目标使用 `myip.ipip.net` / `117.149.203.79` 与 `stun.miwifi.com` /
`111.206.174.2`，TLS 证书验证保持开启。国内域名 HTTPS 在国内 DNS 下选择不同入口，
与同 DNS 条件的物理基线对照，固定 IP HTTPS 与 STUN 另行确认直连。可选 `4.ipw.cn`
在物理基线也解析失败，保留失败记录，不计入必过项。

最后强制关闭并重启正常 APK，设置页继续显示已下载规则。重启前仅在更新目录放置一个
模拟中断的自有临时文件，重启配置后该文件已清理，缓存仍可读取；分流选择也保留。
最终首页预选已验证的第 2 条资源，VPN 断开，无客户端 TUN/前台 VPN 服务，无本应用
崩溃记录，临时独立探针已删除。见 `rules-ui-after-restart.json` 和 `final-state.json`。

## 构建与证据

- APK：`build/app/outputs/flutter-apk/app-debug.apk`，231,374,871 bytes，
  SHA-256 `5de8316c32a8d7f160be9012daa35d2c95eff1d637d8624353a5c0efa1cd76f2`。
- AAR：`android/app/libs/bluegrass.aar`，26,941,520 bytes，
  SHA-256 `f64e5b9b57ef02b73ead9e4f950768b03ca50370fe05fb7ae8f17d0cbb2a0b08`。
- ARM64/x86_64，minSdk26、targetSdk36、debug 签名；16KB 对齐检查通过。
- 两种 ABI 的实际 AAR/APK ELF 均含正确内置规则；最终 AAR 依赖信息已更新到
  `native/core/dependencies.txt`，Xray 固定版本不变，无 sing-box 代理内核。

原始日志和结果位于 `.artifacts/auto-update-20261024/`。设备为 Android Studio
`gst_x64` 模拟器（Android 11/API30），使用原有指定测试账号与 SOCKS5 资源，凭据不写入日志或测试包。

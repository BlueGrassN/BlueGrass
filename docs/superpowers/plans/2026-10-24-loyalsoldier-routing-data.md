# 切换到 v2rayN 同源规则

**目标：** 将 Android/Windows 共用的 GeoSite/GeoIP 数据切换为 v2rayN 默认的 `Loyalsoldier/v2ray-rules-dat`，重建并在模拟器验收 Android 正常 APK。

**设计：** 固定已核验发布 `202610232206` 的 `geosite.dat` 与 `geoip.dat`，校验 GitHub release digest 和下载后的 SHA-256。复用现有确定性提取器生成 CN/GEOLOCATION-!CN 域名与 CN IPv4 子集；沿用已有规则优先级、分流选择、FakeIP/缓存和生命周期。记录完整来源、发布标签、生成校验值与上游许可证。规则随构建内置，不新增运行时自动更新。

- [x] 核实发布、来源与许可，下载并校验原始数据。
- [x] 更新生成器、元数据、规则包与针对真实新数据差异的回归；运行共享路由和 Android 原生测试。
- [x] 更新说明与 Android 版本，复核改动并重建双 ABI AAR/正常 APK。
- [x] 模拟器验证最终 JNI、全局/分流的国内外 HTTPS 与 UDP、选择持久化；保存独立 UID 探针结果。
- [x] 交付 APK 与验收报告；保持模拟器 VPN 断开，清理临时探针。

使用现有指定测试账号；凭据只在内存读取，不输出到代码、包或日志，不更改生产资源。保留当前未提交工作与之前的验收记录。共享规则源码供两端后续构建使用，本轮交付 Android 安装包。

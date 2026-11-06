# Android 规则自动更新

用户要求将刚切换到 Loyalsoldier 的规则自动更新。沿用 Flutter/Kotlin/Go/Xray；本轮交付 Android，Windows 的内置加载方式保持兼容。

实现选择：直接读取官方 GitHub release 元数据和固定发布文件，校验大小、SHA-256、protobuf 与分类后原子持久化。相比只随 APK 更新，这能独立获取规则；相比自建更新服务，无需新增服务端部署。以下默认值由实现选择：每天后台检查一次、启动/回到前台补查，失败至少间隔一小时自动重试，手动检查可立即触发。使用 Android JobScheduler 的联网约束与持久化任务，系统可以延后后台执行。

更新中的 VPN 使用原有不可变快照；下次新连接使用更新后的快照。只在完整成功后替换，旧缓存/内置规则可恢复；不接受版本倒退，不从外部输入接收任意下载地址。保留现有国内外域名优先级、DNS/FakeIP、保护和模式选择。只从官方 HTTPS 端点下载；连接存在时可经所选节点获取，其他情况走应用物理网络。下载错误只影响更新状态。

设置页展示自动更新频率、规则发布版本、最近检查/更新时间、固定错误提示和“立即更新”。活动连接期间解释下次连接生效。不会自动断开 VPN。

Go JNI 契约：`ConfigureRoutingRules(directory string) error`；`RoutingRulesStatus() string`；`UpdateRoutingRules(force bool) string`；`CancelRoutingRuleUpdate()`。JSON 字段：`version`(12位发布标签)、`source`(bundled/downloaded)、`checkedAt`/`updatedAt`(UTC RFC3339 或空串)、`updating`(bool)、`lastError`(空串或固定错误码)。状态读取不进行网络 I/O。方法通道 `bluegrass/routing-rules`：`status`、`update`（force=true，后台线程执行）。

验证覆盖坏哈希/截断/非法分类/版本倒退、掉电中间态恢复、并发检查、会话快照、调度/取消、界面加载与错误、真实设备 JNI 和官方下载、最终两模式 HTTPS/UDP。保留原有测试账号，不输出凭据；不提交或部署其他项目。

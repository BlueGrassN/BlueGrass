# BlueGrass Windows · FlClash 风格界面重做 Implementation Plan

> 已完成（2026-11-05）：界面已集成，静态分析无问题，384 项 Flutter 测试、6 组布局截图和 2 项 Windows 集成测试通过，生产入口版本 0.1.0-beta.1+20 已打包。完整交付与证据见 [界面改版记录](../../../Windows/docs/flclash-ui-redesign.md)。

**Goal:** 把 `Windows/app` 的 Flutter 界面整体改成 FlClash 最新版（v0.9.2）的外观，BlueGrass 自有功能、文案含义和行为全部不变。

**Architecture:** 主题与 surface 体系集中在 `lib/app_theme.dart` 与 `lib/ui/kit.dart`；导航外壳在 `lib/ui/navigation.dart` 与 `lib/main.dart`；每个页面是 `lib/pages/*.dart` 里的 `part of '../main.dart'` 扩展（`extension _XxxPage on _ShellState`），可以直接用 `c`、`page`、`_manualConnect` 等私有成员；刷新状态用 `_update(() {...})`，不要在扩展里直接调用 `setState`。

**Tech Stack:** Flutter 3.47.7 / Dart 3.13.5，Material 3，不新增 pub 依赖。

**Spec:** 用户 2026-11-03 的要求：“整个界面都向 FlClash 靠齐，但是功能还是得是我们自己的，其他的你自己看着办”。FlClash 参考规格：`Windows/.artifacts/flclash-reference/flclash-v0.9.1-ui-spec.md`（第 11 节为 v0.9.2 差异）；v0.9.2 关键源码：`Windows/.artifacts/flclash-reference/v0.9.2-lead/lib/`；v0.9.1 全量源码：`.../flclash-reference/src/`（先读 `src/.agents/surfaces.md`）；官方截图：`.../flclash-reference/images/v0.9.2_snapshots_preview_{light,dark}.png`。

## Global Constraints

- FlClash 是 GPL-3.0：只参考外观和数值，自己重写，不粘贴其源码。
- 功能、校验、错误码、确认弹窗、禁用条件、文案含义保持原样；不新增业务功能，不伪造数据（缺数据显示 “—” / “暂未获取”）。
- 种子色用 BlueGrass 品牌青 #087E98，vivid 风格（已固化在 `app_theme.dart`）；字体 Microsoft YaHei。
- 不画结构性边框：层级靠 `SurfaceLevel` 明度差（page → card → block → inset）；分隔线一律 `colors.hairline`；选中用主色勾徽章或实心主色分段，不用整块主色底。
- 圆角全部超椭圆（`AppShape.all(r)`）：仪表盘卡 32、列表组 24、节点卡 20、侧栏行 10、弹窗 34、按钮胶囊。
- 字阶用 `AppTextStyle`（iOS 字阶：display 40、title1 28、pageTitle 24、title2 22、headline 17、subheadline 15、footnote 13、caption1 12、caption2 11）。
- 现有测试依赖的控件类型、文案和 key 尽量保留（`FilledButton '连接'/'断开连接'/'取消连接'/'恢复并断开代理'`、`FilledButton '选择资源'`、`SegmentedButton<String>` 路由、`SegmentedButton<bool>` 资源范围、资源行 `ListTile` + `OutlinedButton '选择'/'已选择'/'暂不可连接'` + tooltip `'资源详情'`、设置页 `SwitchListTile`、`ValueKey('connection-mode')`、工具栏三个 key 与 48 高`）。只有断言旧视觉细节（例如“两个按钮同尺寸”“TUN 是 OutlinedButton”）的测试可以按新设计改写，并保留其行为意图。
- 集成测试 `integration_test/windows_test.dart` 用 `find.widgetWithText(FilledButton, '连接')`、`'断开连接'`、`find.text('退出账号').first`、`find.text('设置').first`，必须继续成立。
- 布局断点：窗口 < 840 用底部悬浮 Dock，≥ 840 用侧栏（默认展开 220，可收起到 48）；内容区 < 600 时边距 16，否则 24。
- 原生标题栏保留（关闭到托盘、贴靠、缩放行为不变），由 lead 把标题栏颜色调成侧栏 chrome 色。

## Review Focus

1. 窄窗口与 130% 字号：仪表盘卡片、出口卡里的长资源名、长备注、长到期时间不得溢出（`ui_layout_test` 覆盖 480/900/1280/1920 与 1.3 倍）。
2. 连接中 / 已连接 / 忙碌 / 保护暂停时，模式开关和路由分段必须禁用，并显示“请断开连接后切换…/请稍候…”说明。
3. 深色模式：所有新面板在深色下层级可辨、文字对比足够。
4. 无资源、资源已过期、统计不可用时首页不崩、不显示假数据。
5. 键盘焦点：新做的可点卡片/磁贴仍可用 Tab 聚焦并回车触发（用 InkWell/按钮实现即可）。

---

### Task 1（lead，已完成）：主题、UI 套件、导航外壳、页面拆分
- `lib/app_theme.dart`、`lib/ui/kit.dart`（SurfaceLevel/SurfaceScope、BlueGrassSchemeColors（chrome/hairline/tonalFill/success*/warning*）、AppCorner、AppShape、AppTextStyle、AppShadows、BgCard、DashCard、DashCardHeader、StatusChip、GlyphWell、SectionGroup、PageHeader、HeaderAction、BgNotice）、`lib/ui/navigation.dart`（AppSidebar、AppDock）、`lib/main.dart` 外壳与 `lib/pages/*.dart` 拆分。全部测试通过。

### Task 2（home）：首页仪表盘
**Files:** Modify `lib/pages/home_page.dart`；Create `lib/ui/start_button.dart`、`lib/ui/speed_chart.dart`；Tests `test/resource_ui_regression_test.dart`、`test/routing_mode_ui_test.dart`、`test/resource_visibility_test.dart`（只改首页相关断言）。
布局（仪表盘列宽上限 440，间距 16；内容宽 ≥ 860 四列，640–860 两列，< 640 单列）：
- 第 1 行：**状态卡**（2 列，最小高 208）+ **上传卡** + **下载卡**。状态卡：头部 “状态” + 连接状态 StatusChip（已连接 success 带圆点 / 连接中 warning / 其余 neutral，文字用 `c.connectionLabel`）+ 右侧模式文本（保留 `ValueKey('connection-mode')` 与原取值逻辑）；国家徽章（四叶草或超椭圆 well，写 countryCode）；资源名 title1 28 粗体（放不下缩小到 75% 再省略）；chip：地区/城市、协议；两格：“节点出口 IP”（原取值）与 “本地 HTTP 代理”（有值才显示）；底部 `FilledButton.tonal` “选择资源”（跳到我的 IP）；右侧 120 宽槽位放 **启动按钮**：104 dp、填 primary、阴影 primary 30% (0,6) blur 14；未连接为圆形 + 播放图标，已连接/连接中为 9 角饼干形 + 暂停/停止图标，450 ms easeOutBack 形变；按钮是 `FilledButton`，图标下方保留原文案（连接/断开连接/取消连接/恢复并断开代理，FittedBox 缩放），禁用条件与 onPressed 逻辑原样；背景装饰：卡片右上的大饼干形，未连接 block 色，已连接 primary 10%。出口 IP 暂未获取时的提示文字保留。
- 上传/下载卡：头部箭头（下载用 primary）+ 标题 + 右侧 “本次 x”；主数字 display 40 粗体 + 单位（KiB/s，onSurfaceVariant），无数据 “—”；底部 88 高面积图（最近 30 个真实采样，线宽 2、下方 14% 填充，下载 primary、上传 primary 45%，无坐标轴）；只在 `c.traffic.available` 时采样，无数据不画假曲线。
- 第 2 行（64 高）：**路由模式条**（2 列：图标 + “路由模式” + 原 `SegmentedButton<String>` 撑满，分段按 FlClash 连体按钮样式）+ **TUN 模式磁贴**（开关；key `ValueKey('tun-mode-button')` 放在 Switch 上；副标题 “已开启/已关闭（系统代理）”；禁用条件同原 TUN 按钮）+ **允许局域网磁贴**（开关，等价于设置页同一偏好，连接中禁用）。禁用时的说明文字（“请断开连接后切换路由或 TUN 模式。”/“当前操作尚未完成，请稍候再切换模式。”）放在这一行下方。
- 第 3 行：**到期时间卡**（2 列：完整 `_expiry(...)` 文本，title3）+ **本次流量卡**（2 列：总量 + 原说明文字）。
- 末尾 footnote 原样保留。
测试：把“两个按钮同尺寸”“TUN 是 OutlinedButton/FilledButton”这类断言改为新结构（启动按钮仍是 FilledButton；TUN 用 `tester.widget<Switch>` 读 value/onChanged）；保留“选择资源跳转到我的 IP”“连接中锁定模式”“127.0.0.1:12808 只出现一次”等行为断言。

### Task 3（resources）：我的 IP 页与资源详情
**Files:** Modify `lib/pages/resources_page.dart`、`lib/owned_resources_view.dart`、`lib/resource_presentation.dart`、`lib/expiry_reminder_view.dart`；Tests 只允许改 `test/resource_toolbar_test.dart`、`test/customer_remark_ui_test.dart`、`test/owned_resources_view_test.dart`（`resource_ui_regression_test.dart` 归 home，需要改时找 lead）。
- 工具栏：三个控件保持 key 与 48 高；`SegmentedButton<bool>` 用 FlClash 分组 chip 样式（选中实心 primary，未选 tonal，胶囊）；搜索框是填充胶囊；地区下拉同为填充胶囊。
- 资源行：每条一张节点卡（raised 填充、圆角 20、无边框、无左侧色条），内部仍是 `ListTile`（标题行资源名 + 同行备注、副标题两行、错误文案 error 色）；前导国家代码 GlyphWell 40；选中项在徽章角上加 22 dp 主色勾徽章，按钮文字“已选择”；按钮 `OutlinedButton`（主题已是 tonal 胶囊）；详情 IconButton tooltip 不变。
- 空状态、加载、加载更多、官网链接按 FlClash 空状态/胶囊按钮样式。
- 全部资源视图（OwnedResourcesView）：说明文字做成 footnote，订阅/资产分段同 chip 样式，行用节点卡，只读详情用 BgCard；文案不变。
- ResourceDetails 弹窗：保持 AlertDialog（title 不变），字段做成分组块（label caption1 + 值 subheadline），按钮胶囊。
- 到期提醒入口卡（ExpiryReminderView）与清单弹窗：FlClash 卡片 + warning chip。

### Task 4（settings）：设置、诊断、关于、登录与设置类组件
**Files:** Modify `lib/pages/settings_page.dart`、`lib/pages/diagnostics_page.dart`、`lib/pages/about_page.dart`、`lib/pages/login_page.dart`、`lib/update_settings.dart`、`lib/crash_report_settings.dart`、`lib/entry_dns_settings.dart`、`lib/update_presentation.dart`（仅 UpdateReadyBanner 等界面部分）、`lib/connection_artwork.dart`；Tests 只允许改 `test/lan_settings_test.dart`、`test/entry_dns_settings_test.dart`、`test/crash_report_settings_test.dart`、`test/update_settings_test.dart`、`test/update_startup_ui_test.dart`、`test/protected_login_ui_test.dart`、`test/ui_contract_test.dart`、`test/widget_test.dart`。
- 设置页：FlClash 分组列表（`SectionGroup`：组标题 footnote、行最小高 56、前导 24 图标、hairline 分隔、组圆角 24、组 footer 写说明）。外观组保留 `SegmentedButton<ThemeMode>`；网络组保留路由 `SegmentedButton<String>` 与所有 `SwitchListTile`；入口 DNS 行；启动与窗口组；更新与崩溃报告各自成组。
- 诊断页：说明 + 胶囊按钮（开始诊断 / 复制诊断信息）放在一张卡里；结果是分组列表，尾部用 StatusChip（通过 success / 需处理 warning / 未检测 neutral），文案不变。
- 关于页：FlClash 关于页 hero（品牌字标、应用名、版本信息胶囊、简介），下方分组行“开源许可”“官方网站”，再下方更新组件，最后隐私说明 footnote。
- 登录页：灰色页面上居中一张 raised 卡（最宽 400，圆角 28），品牌字标、欢迎文案、FlClash 风格输入框（圆角 16 描边，聚焦主色 2px）、胶囊登录按钮、注册/找回密码、短信登录（预留，仍为禁用 OutlinedButton）；宽屏可在左侧保留简洁的品牌插画区（ConnectionArtwork 改成 FlClash 色调）。所有按钮文案、禁用条件、重试入口不变。
- UpdateReadyBanner、更新设置卡、崩溃报告卡、入口 DNS 弹窗：卡片/分组/胶囊按钮样式，文案和交互不变。

### Task 5（lead）：标题栏配色、收尾与验收
- 原生：`bluegrass_platform.cpp` 增加 `setWindowChrome`（DWMWA_CAPTION_COLOR / DWMWA_TEXT_COLOR，Win11 生效，Win10 忽略），Flutter 端随主题切换调用。
- 清理 main.dart 未用的旧辅助类；全量 `flutter analyze`、`flutter test`；`ui_layout_test` 生成 48 张截图逐张检查；`build.ps1` 打包；写 `Windows/docs/flclash-ui-redesign.md` 并更新 README。

# BlueGrass Android 第一批实现

> 历史记录说明（2026-10-19）：本文保留原日期的方案、缺口和测试/构建事实；其中 sing-box 主内核、旧协议全量承诺及 libbox 路线已由 [ADR 0049](../../adr/0049-xray-only-four-platforms.md) 替代。四端仅使用 Xray-core，旧报告不能作为新核心或其他平台的验收证据。原固定预期出口门槛已撤销，短信为可选能力。

> **For agentic workers:** 使用 executing-plans；Android 原生存储可作为独立任务并行实现，最后统一审阅。

**Goal:** 将现有 Android 模板变成可安装的账号与资源客户端，连接正式 API，交付登录、会话恢复、分页资源、选择、首页和主题设置。

**Architecture:** Android Flutter UI 使用现有 `packages/domain` 与 `packages/data_source` 的纯 Dart 公共边界（暂不移动 Windows 文件）；Android Keystore/AES-GCM 持久化会话，平台通道保存设备 UUID 和主题。仅会话持久化，密码及节点凭据不落盘。当前批次不注册空 VPN 服务或模拟已连接。

**Tech Stack:** Flutter 3.47.7 / Dart 3.13.5；Kotlin；Android API 26+。

**Spec:** 现有主设计、D-18/19/23/24/25/38/39/42 与 README 最新连接规则。2026-10-13 用户明确要求开始 Android 开发；不重开已确认设计讨论。

## Global Constraints

- 品牌 BlueGrass，包名 `com.bluegrass.client`；默认跟随系统主题。
- API 使用 `https://client-api.sunip.cc/api/client/v1`，正常证书校验、禁明文、禁重定向复用既有传输。
- 到期时间 UTC+8；资源键包含环境、账号、类型和源 ID；空页带 nextCursor 仍可继续。
- 默认可连接资源；全部资源按服务器能力加载 subscription/asset，不能声称保守接口等于完整账户快照。
- API 与 Windows 现有文件保持原样；本轮仅写 Android 和本文档，不提交混合工作区。
- APK 是内部开发包；VPN/sing-box、真实线路、后台保护、短信、更新另行实现，禁止伪造连接、出口或流量。

## Review Focus

- 退出和登录代次切换后迟到响应不能恢复账号、列表、选择。
- 安全存储失败不降级明文；退出清理失败可重试且不能报告成功。
- 请求失败、空页、重复游标、重复资源和部分资产页不能误报加载完成。
- 主题写入失败及快速切换时不丢失旧设置、不清空输入或资源。
- 小屏、键盘、大字体、浅深色下登录和列表可以操作；节点认证信息不展示。

## Task 1：Android 平台存储

**Files:** `Android/android/app/src/main/kotlin/com/bluegrass/client/{MainActivity,SecureSessionStorage}.kt`，Manifest、备份 XML、app Gradle；`Android/lib/platform/android_store.dart`，相关测试。

**Interfaces:** MethodChannel `bluegrass/android`：`sessionRead -> String?`、`sessionReplace({value: String}) -> void`、`sessionClear -> void`、`deviceId -> String`、`themeRead -> system|light|dark`、`themeWrite({value}) -> void`。错误仅固定 `storage_error`/`invalid_argument`，不输出异常正文。原子持久化、后台串行 I/O。

- [x] 先验证 Dart 存储往返、非法 JSON 与平台错误传播，再实现桥接；原生增加设备端加密/重启/篡改测试。
- [x] 最低 API 26、INTERNET、禁备份与明文；不注册 VPN。
- [x] Flutter 测试与 Gradle 编译；设备端测试有设备时执行，否则记录未运行。

## Task 2：账号及资源状态

**Files:** `Android/lib/app_controller.dart`、`Android/lib/resource_presentation.dart`、`Android/test/app_controller_test.dart` 及 test/support。

**Interfaces:** `AppController(CustomerDataSource source, AndroidStore store)` 负责启动恢复、登录、退出、refresh/loadMore、资源视图、选择及主题；UI 通过 ChangeNotifier 观察。

- [x] 测试登录预选、退出迟到响应隔离、分页空页与重复游标、全资源双类型、错误重试、UTC+8 与到期不可选择。
- [x] 实现有界分页（每次一页）、去重、错误分类；账号令牌完全由现有 SessionCoordinator 管理。
- [x] `flutter test test/app_controller_test.dart` 通过。

## Task 3：可运行界面与构建

**Files:** `Android/lib/main.dart`、`Android/lib/app.dart`、`Android/lib/screens/*`、pubspec、widget tests、README。

- [x] 替换模板测试，覆盖表单校验、登录/导航/选择/退出、主题不重置输入、小屏大字以及无伪造连接。
- [x] 实现 Material 3 中文登录、首页、分页资源/详情和设置，真实启动入口注入 ApiV2DataSource。
- [x] `flutter analyze`、`flutter test`、共享两包测试、`flutter build apk --debug --target-platform android-arm64`。
- [x] 独立审阅新增 Android 文件，修复有效问题；记录 APK 和实际验证边界。

## Execution Notes

- 当前工作区 `feature/windows-client` 含尚未提交的 Android/Windows 工程和新契约。沿用原目录，避免新 checkout 遗漏必需的未提交依赖；不改分支或提交其他工作。
- 本机暂未发现连接的 adb 设备；编译和 Flutter 测试不能代替 Android Keystore 与 VPN 真机验收。

## 完成记录

第一批代码、测试、预览和 APK 已完成；完整证据见 Android/docs/verification.md。23 项 Android 测试、163 项领域包测试、179 项数据源测试通过；设备测试包编译通过但 16 项设备用例未运行。VPN 为后续批次，未将其勾选为完成。

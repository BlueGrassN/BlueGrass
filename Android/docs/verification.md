# Android 第一批验证记录

日期：2026-10-13（Asia/Hong_Kong）。本轮为本地开发，基于包含未提交 Windows/API 工作的 checkout；没有提交、推送、生产部署或生产数据库操作。

## 已实现

- Android Flutter 示例替换为 BlueGrass：密码登录、恢复会话、退出、可连接资源分页与预选、手动选择、订阅/资产视图、当前已加载资源搜索、首页及主题设置。
- 生产 main 使用现有 `ApiV2DataSource`，请求目标 `https://client-api.sunip.cc/api/client/v1`；不含 fixture 切换开关。
- 复用既有协议及会话规则；节点入口不当作实际出口，缺少预期出口不阻止资源选择。未实现 VPN 时连接按钮不可用，不制造连接/出口/流量。
- Android API 26，包名 `com.bluegrass.client`，显示名 BlueGrass，新增原生自适应草叶图标。
- 会话通过 AndroidKeyStore AES-256-GCM 加密，随机 IV、带认证标识、受限长度、后台串行原子写入。设备 UUID/主题和加密会话位于 noBackupFilesDir，禁备份和明文 HTTP。

## 已执行的检查

| 检查 | 结果 | 证明范围 |
| --- | --- | --- |
| `flutter analyze` | No issues found | Dart 静态检查 |
| `flutter test` | 23/23 通过 | 控制器、界面、平台通道边界及真实 API 适配器组合 |
| `dart test`，Windows/packages/domain | 163/163 通过 | 当前复用的领域包 |
| `dart test`，Windows/packages/data_source | 179/179 通过 | 当前复用的 API、资源、刷新与会话包 |
| `flutter test tool/render_previews.dart` | 1/1 通过，四张图片已人工检查 | 虚构资源下的实际 Flutter 渲染；不等于设备截图 |
| `gradlew :app:assembleDebugAndroidTest -Ptarget-platform=android-arm64` | BUILD SUCCESSFUL | 16 项设备测试及原生实现编译、测试 APK 打包；没有执行测试 |
| `flutter build apk --debug --target-platform android-arm64` | 最终构建通过；包信息见下方产物记录 | 可构建性，不证明生产登录和设备 VPN 能力 |

测试使用明确假账号与假令牌，没有读取本地真实测试账号或访问生产账号。真实 API 适配器测试在传输边界提供契约响应，验证登录 DTO、账号/设备名称、认证头、存储恢复和退出；不能称为真实网络联调。

## 审阅修复

独立审阅提出五项问题，均已补测试，先观察六项失败再修复，全套 23 项最终通过：

1. 过时资源视图触发当前账号失效：先处理当前账号认证丢失，再过滤过时查询结果；安全存储清理失败返回可重试的退出状态。
2. 刷新覆盖手选资源：保存完整 ResourceKey，等待对应后续页；目标缺失不擅自改选其他资源。
3. 临时能力接口失败后不能恢复：用户刷新时重新取能力，成功后恢复订阅/资产页入口。
4. 初始化恢复旧主题覆盖新选择：成功写入的主题版本阻止旧读取结果覆盖。
5. 终止页仍携带 cursor：视为同步错误，不静默当作完整列表。

审阅范围覆盖 Dart 控制器/UI、API 适配、原生安全存储和配置。修复通过回归测试确认，没有以第二次静态审阅代替测试。

## 构建环境与限制

- Flutter 3.47.7、Dart 3.13.5，Android Studio JDK 25.0.3；工具路径见 README。
- 首次 Gradle 启动遇到 JDK 运行库 DLL 加载失败。独立 java 可启动；重建本进程 Machine/User PATH 并设置 JAVA_HOME 后 APK 成功构建。
- 默认设备测试构建尝试拉取本机缺少的 ARM32/x86_64 Flutter Maven 包，因 storage.googleapis.com DNS 失败而中断；使用明确的 `-Ptarget-platform=android-arm64` 后通过。
- Gradle 自动安装所需 CMake 3.22.1，保留已存在的 4.1.2；以实际可执行文件版本及后续构建结果复核。
- JDK 原生访问提示、SDK cmdline-tools/latest-2 的位置提示、平台测试框架弃用提示未阻止构建；没有为消除提示改动全局 SDK 布局。
- 原生编译检查的临时目录 `.native-storage-check/` 因自动审批拒绝清理而保留，已从 Git 排除；不包含业务秘密。

## 尚未验证或实现

adb 当前没有连接设备。16 项 AndroidKeyStore 设备测试、跨进程/设备重启、掉电持久化、Activity 重建和设备字体/键盘仍未实测；测试中新建存储对象不能替代这些验证。

本批未实现 VpnService、sing-box 转发、后台/断线保护、DNS/IPv6/切网、流量统计、实际出口探测、短信登录、更新和正式签名。真实生产登录/资源获取也未验证。下一批先完成受控设备上的 VPN 连接闭环。

## 最终产物

- 文件：`Android/build/app/outputs/flutter-apk/app-debug.apk`
- 类型：内部 ARM64 debug 开发包，版本 `0.1.0+1`
- 大小：93,777,853 字节
- SHA-256：`2727AF93487F97FCC5E14D365A1A14ABB5BD8486C14AB0701C805CFF611AEB3D`
- 最后构建：`flutter build apk --debug --target-platform android-arm64`，退出码 0，12.0 秒。
- aapt 核对：`com.bluegrass.client`、BlueGrass、targetSdk 36、arm64-v8a；Manifest 最低 API 与安全标志另经 xmltree 核对。
- CMake 实际命令返回 `cmake version 3.22.1-g37088a8-dirty`，最终构建成功。

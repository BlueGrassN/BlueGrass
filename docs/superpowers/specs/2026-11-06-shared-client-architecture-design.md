# BlueGrass 跨端共享架构

用户已确认：采用共享 Flutter 界面、业务逻辑和 Xray 核心，系统能力按平台实现；先抽公共包，再合并 Android 与桌面的控制器、页面和组件，手机底部导航、桌面侧栏，逐端验证后上传 main。

## 目标布局

- `packages/domain/`：原 `Windows/packages/domain`，平台无关领域规则。
- `packages/data_source/`：原 `Windows/packages/data_source`，统一 API/会话/资源查询。
- `packages/client/`：所有端共同依赖的 Flutter 客户端库，保留包名 `bluegrass_client`。共享页面内容、主题/组件和可复用控制逻辑；Android VPN 与桌面 IPC、系统权限等留在明确的平台适配层。
- `core/`：原 `Windows/native/core`，Go 模块名继续使用 `bluegrass/native/core`，Xray 及依赖锁保持不变。
- `Windows/app`、`Android`、`Mac/app`：平台宿主与薄入口；必要的旧 Dart 路径只保留导出兼容层，不维护第二份实现。三个宿主共同消费 `packages/client`。
- Android 原生桥接模块依赖根 `core`；Windows/macOS 专用 Go 实现继续由现有 build tags/宿主选择，不重写系统网络事务。

- `contracts/`：Go 与 Dart 共同使用的 schema 和测试向量，来自原 Windows/contracts。

## 行为与约束

现有登录、会话隔离、资源分页/选择、过期和恢复保护、流量、分流、诊断、桌面更新/提醒/崩溃记录、系统代理/TUN/VPN 继续可用。共享层不直接决定 OS 权限、VPN fd、WFP/PF 或平台安全存储。采用能力接口和组合，不把平台区别散落到每个页面。

迁移必须产生实际被多端调用的共同控制逻辑和共同页面/组件，不能只把两套实现放到同一目录。桌面和手机保留适合各自屏幕的布局、交互以及已有平台专属选项，公共功能改动有唯一实现位置。

保留原包 ID、签名、Go 模块名、Xray 版本、品牌字节、各端版本编号和固定 `preview` Release 规则。构建、校验、CI 路径触发器和开发文档随目录调整。未实现 iOS/HarmonyOS 客户端不在本轮伪造支持。

## 验收

共享业务、API、客户端组件和三端原有 Dart 回归通过；Go 全模块与 Android 桥接测试通过；Windows 打包与 Android APK 构建通过；macOS GitHub 双架构构建/验证通过。根共享目录改动能触发相应平台构建，统一发布器继续只使用可信 main 成功产物。真实设备联网/权限恢复不由编译或单元测试替代。

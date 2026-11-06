# 跨端共享客户端开发

2026-11-06 起，公共实现放在仓库根目录，由 Windows、Android、macOS 平台工程共同引用。平台工程仍负责各自原生宿主和发行身份，公共库不是另一个可单独发布的应用。

## 源码归属

- `packages/domain/`：资源身份、连接资格、时间/到期、分页、流量等纯 Dart 规则。
- `packages/data_source/`：API、长期会话、刷新令牌、资源查询和传输接口。
- `packages/client/`：包名 `bluegrass_client`，共同的 Flutter 客户端实现。共享功能、组件和业务协调只维护一份；桌面原生 IPC 与 Android VPN/MethodChannel 保留适配边界。
- `core/`：唯一 Go/Xray 核心模块，Go module 名仍为 `bluegrass/native/core`。通用协议/规则/生命周期和按操作系统隔离的 WFP/PF/utun/服务实现都在这里，模块路径与物理目录不必同名。
- `contracts/`：Go 与 Dart 共用的 schema 和测试向量，禁止为平台复制样本。
- `Windows/app/`、`Android/`、`Mac/app/`：薄 Flutter 宿主、原生平台工程、平台回归和打包入口。Windows 宿主包名为 `bluegrass_windows`；Android 的 `client`、Mac 的 `bluegrass_mac` 以及应用 ID、发行文件名保持原值。

Windows/app/lib 和 Android/lib 的旧 Dart 导入路径保留为导出层，方便既有测试和开发入口迁移；实现位于 packages/client。不要在这些导出文件里恢复独立页面或业务代码。Mac/app/lib/main.dart 直接调用公共客户端的桌面入口。

## 共享与平台边界

账号/API、资源展示与共同交互由公共包承担，平台控制器通过组合复用业务协调。Android VPN 服务与桌面子进程的连接、暂停、权限授权、故障保护和恢复确认仍由各自实现负责；共享 UI 不能自行清除保护或假定某种平台能力存在。

手机使用适合窄屏的导航和页面组合，桌面保留侧栏、窗口、托盘及额外设置。公共功能的新字段或交互首先修改公共实现，再由能力/数据适配器接入，避免把重复代码分别放进手机和桌面页面。

系统代理、WFP/PF、管理员授权、后台 VPN、安全存储、自启动、签名和更新策略必须保留平台边界。Windows 的提醒/更新/崩溃记录及 Mac 的手动替换开发包流程，不因为界面共享而被删除。Android 的旧 VPN 回调、不同账号资源和迟到请求仍必须隔离。

## 工具链与本地验证

Flutter 统一为 3.47.9，Dart 3.13.7，Go 保持 1.27.2；Xray 锁保持原版本。宿主与公共客户端的精确 dependency_overrides 保持一致，锁文件随依赖路径调整提交。

纯业务包分别在 `packages/domain`、`packages/data_source` 执行 `dart pub get`、`dart test`。公共 Flutter 包在 `packages/client` 执行 `flutter pub get`、`flutter analyze`、`flutter test`。随后运行实际宿主回归，不能用公共包通过代替平台集成。

仓库根目录的原有一键入口继续可用：

```powershell
./Windows/scripts/check.ps1
./Windows/scripts/package.ps1
./Android/scripts/build-release.ps1
python Mac/tools/sync_assets.py --check
python -m unittest discover -s Mac/tools/tests -v
```

Go 通用模块测试从 `core/` 执行 `go test ./...`；Android 原生桥接测试仍在 `Android/native/core/` 执行，go.mod 的 replace 指向根 core。macOS 最终打包和系统专属测试通过 GitHub 双架构工作流执行，真实联网与权限恢复由平台设备验收。

## 构建与发布

Windows/macOS 工作流的路径过滤包含根 `packages/**`、`core/**`、`contracts/**`，Android 仍按既有规则在 main 推送时构建。三端都验证公共包，依赖缓存和锁文件检查使用新路径。

产物目录、平台版本号、Android 签名及固定 [preview Release](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview) 维持原发布契约。详见[统一发布手册](github-releases.md)。仅移动目录不会重置构建号，也不会新增平台 tag。

原始品牌、许可证和路由数据仍按哈希固定；目录迁移不能重算预期哈希来掩盖字节变化。旧日期的验证记录描述当时工程状态，不能替代迁移后的回归与构建记录。

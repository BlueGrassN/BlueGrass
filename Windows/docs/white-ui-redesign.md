# 白色界面更新（2026-10-15）

按要求先保存旧界面，再重做 Windows 客户端界面。代码和产物均在 `Windows/` 内。

## 旧版备份

备份目录：`Windows/backups/ui-before-white-redesign-20261015-163651/`。

- `source.zip`：重做前的界面源码、资源、Windows 宿主、共享包、原生代码、测试和构建脚本，共 538 个文件。
- `BlueGrass-Windows-x64-beta1-build12.zip`：重做前的完整可运行包，版本 `0.1.0-beta.1+12`。
- `manifest.json`：上述两个文件的大小和 SHA-256。
- `README.md`：恢复说明。

备份不包含测试账号文件、本机会话、构建缓存或日志。备份目录被 Git 忽略，但保留在本地；它位于 `dist` 之外，不受自动解压替换影响。

需要查看旧界面时，先从托盘退出正在运行的 BlueGrass，将备份运行包解压到另一个独立目录，再运行其中的 `BlueGrass.exe`。需要恢复源码时，将 `source.zip` 解压到临时目录并比对；包内路径相对于 `Windows/`，不要直接覆盖新的修改。

## 新界面

- 以白色为主，深灰文字、青蓝色操作强调；主面板采用细线分区，控件统一为 4px 小圆角。
- 侧栏使用细分隔线和左侧选中标记，资源国家标识改成方形。
- 登录页采用独立表单与线框网络插画，去掉大圆角外框。
- 首页分为连接状态、资源信息、操作区和流量统计；两个操作按钮保持同样尺寸和样式，全局接管开关仍紧邻按钮。
- 我的 IP 使用紧凑的列表行与明确的选中标记；资产名称和客户备注继续分别显示。
- 设置按外观、网络连接、启动与窗口分区；诊断、关于、提醒、详情弹窗同步采用统一主题。
- 保留浅色、深色、跟随系统以及既有偏好。连接资格、出口检测、12808 固定端口、72 小时隐藏规则、官网链接、API 远程切换、任务栏图标修复保持原有行为。

主题集中在 `app/lib/app_theme.dart`，登录插画在 `app/lib/connection_artwork.dart`；没有引入新的依赖、页面路由系统或重复的主题框架。插画为装饰，不代表实时网络状态。

## 验证

- Flutter 全量测试：354 项通过。
- `flutter analyze --no-pub`：无问题。
- 新增页面布局回归：480、900、1280、1920 像素宽度，720 像素高度；另外覆盖 130% 字体和深色模式。
- 覆盖登录、未连接/已连接/无资源首页、我的 IP、资源详情、设置、诊断和关于。既有测试继续验证备注位置、按钮同尺寸、全局接管切换约束、官网链接和资源过期规则。
- 页面截图使用模拟数据，不接管网络、不调用生产登录。截图加载 Windows 微软雅黑与 Material 图标字体。

复现截图（在 `Windows/app` 执行）：

```powershell
flutter test --no-pub test/ui_layout_test.dart --dart-define=BLUEGRASS_UI_SCREENSHOTS=E:/workspace/APP/Windows/.artifacts/screenshots/white-ui
```

证据文件：

- `Windows/.artifacts/verification/white-ui-flutter-tests.log`
- `Windows/.artifacts/verification/white-ui-analyze.log`
- `Windows/.artifacts/screenshots/white-ui/`（48 张页面截图）

本次是界面改版；以上布局验证不替代真实网络、签名安装器或 TUN 的发行验收。

## 构建与查看

使用正式入口 `lib/main.dart` 打包，版本 `0.1.0-beta.1+13`。

```powershell
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 13
```

压缩包仍为 `Windows/dist/BlueGrass-Windows-x64-dev.zip`。脚本先删除旧的 `Windows/dist/BlueGrass-Windows-x64/` 解压目录，再解压新包；可直接运行其中的 `BlueGrass.exe`。打包不会删除上述旧界面备份。

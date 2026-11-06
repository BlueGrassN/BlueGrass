# Windows 界面改版：FlClash 风格

后续 build21 已将首页资源选择改为就地弹出面板；最新入口见[交互更新](home-resource-picker.md)。下文保留 build20 整体改版记录。

日期：2026-11-05。

## 外观与功能

本轮按用户“整个界面向 FlClash 靠齐、功能仍使用 BlueGrass 自己的”要求，参考 [FlClash v0.9.2](https://github.com/chen08209/FlClash/releases/tag/v0.9.2) 的桌面布局与交互外观重新实现 Flutter 界面。保留 BlueGrass 字标与青色强调色。

- 统一浅色/深色主题：灰色页面、分层面板、连续圆角、胶囊按钮与轻阴影。
- 桌面侧栏支持收起，窄窗口改用底部悬浮导航。
- 首页改为状态与出口卡、连接按钮、上传/下载曲线、路由模式、TUN/局域网控制、到期时间和流量卡。曲线只使用当前连接的真实采样；无数据时显示“—”。
- “我的 IP”改为胶囊筛选与圆角节点卡；保留连接资源/全部资源、搜索、地区过滤、备注、详情和官网入口。
- 登录、设置、诊断、关于、DNS、更新、崩溃报告和到期提醒统一组件风格。
- Windows 原生标题栏随应用主题配色；保留系统拖动、缩放、贴靠及关闭到托盘行为。标题栏自定义颜色由系统版本决定，旧系统可保持系统默认颜色。

控制器、API、领域包与代理内核未随此次 UI 改版改动。连接前确认、连接保护与恢复、资源有效期校验、模式锁定、自启/自动连接、更新策略等使用原有业务逻辑。审阅中修复了 TUN 副标题混淆保存偏好和实际接管状态的问题：活跃连接显示实际模式，未连接显示下次连接配置。

## 验证记录

最终集中验证日志位于 [flclash-ui](../.artifacts/flclash-ui/progress.md)：

- [静态分析](../.artifacts/flclash-ui/final-analyze.log)：无问题。
- [完整 Flutter 回归](../.artifacts/flclash-ui/final-flutter-tests.log)：**384 项通过**；含新增的侧栏折叠/键盘/窗口切换、开关语义树与恢复连接状态文案测试。
- [多尺寸布局和截图](../.artifacts/flclash-ui/final-screenshots.log)：480、900、1280、1920 宽度，130% 字号及深色模式，54 张截图；截图启用真实阴影，最终 **6 组全部通过**。一次中途退出的渲染进程日志保留在 [screenshots-interrupted.log](../.artifacts/flclash-ui/screenshots-interrupted.log)；隔离大字号场景与完整重跑均通过，没有留下未解决的布局失败。
- [原生 Windows UI 集成](../.artifacts/flclash-ui/final-native-ui.log)：**2 项通过**；验证本地凭据存储，模拟账号登录、选择、连接/断开、主题切换及退出账号，并保存[原生窗口截图](../.artifacts/screenshots/flclash-native/home-light.png)。
- [独立代码审阅](../.artifacts/flclash-ui/review.md)：确认的问题已修复；开关缺少名称的初步推断被实际语义树证据推翻，未增加重复标签。
- [便携启动器/桌面集成自检](../.artifacts/flclash-ui/final-portable-tests.log)：启动器参数传递、中文/空格路径、自启及普通通知注册通过；便携通知注册遇到一次 HRESULT 80070020 文件共享占用，同一测试程序随后[隔离复测通过](../.artifacts/flclash-ui/portable-notification-recheck.log)，无需改动通知实现。

预览使用显式模拟数据，未接管系统网络。以上 UI 验证不替代真实线路、管理员 TUN/WFP、重启恢复或签名发行的独立验收。

## 界面预览

[浅色首页](../.artifacts/screenshots/flclash-final/1280-1.0-light-connected-traffic.png) · [深色首页](../.artifacts/screenshots/flclash-final/1280-1.0-dark-connected-traffic.png) · [资源页](../.artifacts/screenshots/flclash-final/1280-1.0-light-resources.png) · [设置](../.artifacts/screenshots/flclash-final/1280-1.0-light-settings.png) · [登录](../.artifacts/screenshots/flclash-final/1280-1.0-light-login.png)

## 构建与备份

[正式构建](../.artifacts/flclash-ui/final-build.log)使用[生产入口](../app/lib/main.dart)，版本 **0.1.0-beta.1+20**，已成功生成便携 ZIP（60,228,070 字节，约 57.4 MiB）并同步解压。顶层启动器和 runtime 客户端版本均已核对；这是未签名的开发便携包。文件 SHA-256 见 [发行清单](../.artifacts/flclash-ui/release-manifest.json)。

在仓库根目录执行：

```powershell
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 20
```

输出便携包 [BlueGrass-Windows-x64-dev.zip](../dist/BlueGrass-Windows-x64-dev.zip)，解压后运行顶层 BlueGrass.exe，旁边的 runtime 目录必须保留。打包脚本同步更新 [已解压的启动入口](../dist/BlueGrass-Windows-x64/BlueGrass.exe)。

改版前的源码、可运行包与 SHA-256 保存在 [备份说明](../backups/ui-before-flclash-redesign-20261103-113500/README.md)。需要回退时先把备份解压到独立目录进行比对。

## 复现检查

在 Windows/app 目录执行：

```powershell
flutter analyze --no-pub
flutter test --no-pub --reporter expanded
flutter test --no-pub test/ui_layout_test.dart --dart-define=BLUEGRASS_UI_SCREENSHOTS=E:/workspace/APP/Windows/.artifacts/screenshots/flclash-final
flutter test --no-pub integration_test/windows_test.dart -d windows
```

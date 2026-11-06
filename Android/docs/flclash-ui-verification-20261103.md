# FlClash 风格 Android 前端验收

日期：2026-11-03（Asia/Taipei）。BlueGrass 版本：0.4.0+7。

## 参照与变更

本次通过官方 GitHub latest release API 确认 FlClash 最新正式版为 [v0.9.2](https://github.com/chen08209/FlClash/releases/tag/v0.9.2)，提交 `7fd417fb9a9a277c6a9339a8ac87f907df7c25f7`。阅读该版本的移动端仪表盘、导航 dock、代理卡片、设置及官方截图。

使用现有 Flutter 组件独立实现，保留 BlueGrass 品牌、账号/API、Xray 原生内核及所有规则更新逻辑。视觉参考包括：34px 圆角仪表盘、分层浅色/深色表面、花瓣形连接按钮、悬浮胶囊导航、代理双列卡片、分组设置。当前 BlueGrass 的实际目的地为仪表盘、代理、设置、诊断，没有增加未实现的订阅编辑或流量图表。

- 首页展示原生确认的连接状态和实际检测出口；连接中、暂停、异常及待切换资源分别处理。
- 代理列表保留搜索、资源范围、分页、选择及详情；窄屏和大字体自动单列。
- 登录/主题/账号退出、全局与国内外分流、规则自动更新与手动更新、脱敏诊断均保留。
- 返回其他标签页时回到仪表盘，不停止当前 VPN。
- 获取资源阶段也可取消；复制诊断后的 SnackBar 位于导航上方。

## 本次验证

- `flutter analyze --no-pub`：无问题。
- `flutter test --no-pub --reporter expanded`：**156 项通过**。覆盖登录、主题、资源范围/分页、选择资源、连接/取消/暂停、出口检测失败、活动资源与待选资源身份隔离、分流锁定/持久化、规则更新失败/轮询生命周期、小屏幕与 1.8 倍字体，以及 2 倍字体配合 48dp 系统安全区。
- 新回归测试实际复现后修复：等待 `getNode` 时缺少取消操作；SnackBar 遮住悬浮导航文字；2 倍字体与较高系统安全区使页面末尾按钮被底栏覆盖。页面现在从 Scaffold 获取底栏实际高度。另新增首页首屏连接/断开与返回导航测试。
- 使用 `tools/render_ui_previews_test.dart` 运行真实 Flutter 页面渲染，检查 390×844 浅色/深色画面；预览使用合成账号和文档用途 IP `203.0.113.42`，不代表真实 VPN 设备截图。中文字体只用于本地预览，不进入安装包。
- 本次没有重开 Android Studio 或模拟器。现有 x86_64 模拟器不匹配 ARM64-only 包，因此没有把本次页面测试说成新 APK 的设备转发实测。历史真实 HTTPS/UDP 与自动更新测试见 [0.3.2 验收记录](rules-auto-update-verification-20261024.md)。

## 页面预览

| 页面 | 浅色 | 深色 |
| --- | --- | --- |
| 仪表盘 | [已连接](previews/flclash-20261103/dashboard-light.png)、[未连接](previews/flclash-20261103/dashboard-idle-light.png) | [深色](previews/flclash-20261103/dashboard-dark.png) |
| 代理 | [双列节点卡片](previews/flclash-20261103/proxies-light.png) | 同一主题系统 |
| 设置 | [浅色](previews/flclash-20261103/settings-light.png) | [深色](previews/flclash-20261103/settings-dark.png) |
| 登录/诊断 | [登录](previews/flclash-20261103/login-light.png) | [诊断](previews/flclash-20261103/diagnostics-dark.png) |

## 证据

最终正常 APK：`build/app/outputs/flutter-apk/app-release.apk`，**20,512,605 bytes（20.51 MB / 19.56 MiB）**，较此前包增加 6,688 bytes。SHA-256：`2ec4243ce74206b0c32416f3148fe4fd95555e2891424f48d81b908208de8989`。

已核对版本 0.4.0 / code 7、最低 API 26、仅 `arm64-v8a`、非 debuggable；ZIP 完整性、APK 签名、与旧包签名身份一致、ZIP 对齐和全部 ELF 16KB 对齐检查通过。三个原生库继续压缩存放，`extractNativeLibs=true`；Xray 共享库和 Flutter 引擎解压后与原包逐字节一致。依赖锁文件未变。

本次日志、原 APK、改动前前端、官方版本元数据与最终 APK 检查位于 `.artifacts/flclash-ui-20261103/`。正常发布入口仍为 `lib/main.dart`，ARM64-only、Release 和原生库压缩设置不变。安装包使用项目已有签名，未引入新的发布签名密钥。

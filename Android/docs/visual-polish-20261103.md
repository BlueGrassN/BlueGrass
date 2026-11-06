# Android 0.4.1 视觉细节修正

2026-11-03。根据用户对 0.4.0“缺少 FlClash 阴影和质感”的反馈进行修正。

## 实际改动

- 卡片改为独立绘制的多层投影，区分贴边、近处及远处阴影。外部阴影不被内部 Material 的裁剪区域切掉。
- 连续曲率圆角、轻微边缘高光；首页右侧增加淡色装饰，连接按钮带柔和蓝色光晕。
- 悬浮导航使用柔和投影、半透明背景及局部模糊。模糊限制在底栏内，不影响页面布局和既有安全区处理。
- 深色主题中的账号、诊断、规则信息等内层表面比卡片略亮，修正之前的黑色凹陷效果。
- 统一节点卡片和登录/设置卡片的视觉层次。未修改控制器、接口、分流或规则自动更新行为。

参照 [FlClash v0.9.2 的卡片与阴影设计](https://github.com/chen08209/FlClash/blob/v0.9.2/lib/views/dashboard/style.dart)及[表面层次](https://github.com/chen08209/FlClash/blob/v0.9.2/lib/ui/surface.dart)，使用 BlueGrass 自有 Flutter 组件实现。

## 预览与验证

预览工具使用实际页面、合成测试账号和文档用途 IP；启用真实阴影绘制，避免 Flutter 普通 widget 测试默认关闭阴影产生的硬边预览。不属于设备网络实测截图。

| 页面 | 浅色 | 深色 |
| --- | --- | --- |
| 仪表盘 | [新版](previews/flclash-polish-20261103/dashboard-light.png) | [新版](previews/flclash-polish-20261103/dashboard-dark.png) |
| 代理 | [新版](previews/flclash-polish-20261103/proxies-light.png) | — |
| 设置 | [新版](previews/flclash-polish-20261103/settings-light.png) | [新版](previews/flclash-polish-20261103/settings-dark.png) |
| 登录/诊断 | [登录](previews/flclash-polish-20261103/login-light.png) | [诊断](previews/flclash-polish-20261103/diagnostics-dark.png) |

156 项现有 Flutter 回归测试通过，覆盖连接/取消、资源身份、分流锁定、规则更新和大字体/安全区。真实阴影渲染流程通过并逐图检查。Android Studio 和模拟器维持关闭，未新增设备转发验证。

独立审查发现的卡片语义分组差异已修复，保留原来 `Card` 的无障碍分组。静态检查通过。模糊仅作用于一个底部导航区域；本轮没有设备帧率测量。

本次证据在 `.artifacts/flclash-polish-20261103/`，包含改动前源码和 APK、测试日志、预览日志及最终包检查。

## 最终安装包

`build/app/outputs/flutter-apk/app-release.apk`：0.4.1+8、ARM64-only、Release、**20,517,329 bytes（20.52 MB）**，仅比 0.4.0 增加 4,724 bytes。

SHA-256：`f2abe9cfb9251e8930532682d94bb94b89d5ebe2b11f09579223d2eeeb0284ae`。

ZIP 完整性、签名、签名身份一致性、ZIP 对齐和 ELF 16KB 对齐通过。Xray 共享库及 Flutter 引擎解压后与 0.4.0 逐字节一致，原生库继续压缩存放；仅应用前端代码发生变化。

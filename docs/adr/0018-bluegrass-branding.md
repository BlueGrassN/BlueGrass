# ADR 0018：BlueGrass 品牌与发行标识

- 状态：已接受。正式名称、Android 包标识和 BlueGrass 品牌方向已确定，具体图标素材在视觉设计阶段交付。
- 决策日期：2026-10-10，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-18。
- 确认依据：用户明确要求应用名使用 BlueGrass，并选择 Android 包标识 `com.bluegrass.client`。

## 已确认的品牌

应用显示名称统一为 **BlueGrass**，沿用精确大小写；安装包展示、窗口标题、托盘、通知和关于页采用同一名称。文档标题与产品文案同步使用 BlueGrass。

图标采用 BlueGrass 品牌识别，分别适配 Android 桌面、Windows 桌面和托盘；具体图稿与导出资产在视觉设计阶段完成。当前仓库没有可直接复用的图标资源，本记录不表示图标已制作或验收。

## 已确认的发行标识

| 项目 | 标识 | 用途 |
| --- | --- | --- |
| 应用显示名称 | `BlueGrass` | 窗口、桌面、托盘、通知和关于页 |
| Android applicationId | `com.bluegrass.client` | APK 安装身份及更新校验 |
| Android namespace | `com.bluegrass.client` | Android 宿主与原生插件的工程命名空间 |
| Windows 服务名 | `BlueGrassService` | 后台服务的内部名称，按已确认品牌派生 |
| Windows 服务显示名 | `BlueGrass 后台服务` | 系统服务管理界面 |
| Windows 命名管道 | `\\.\pipe\BlueGrass.Control` | 受鉴权保护的界面与服务 IPC |
| Windows 服务数据目录 | `%ProgramData%\BlueGrass` | 服务状态、日志和受控缓存 |

工程初始化时按本表设置 Android 身份，安装包检查、更新清单和发布说明统一使用 `com.bluegrass.client`。stable/beta 通过发行通道区分，不在本轮自动增加其他包名。Windows 内部名称按 BlueGrass 品牌统一派生，实际服务和管道权限仍按 S5/S6 验证。

## 业务数据源关系

BlueGrass 首版仍对接既定的太阳IP客户中心与网页取数流程，并预留其后续 V2 API。品牌名称变更不表示业务网站地址、账号体系或服务端协议已经变更。

## 验证要求

构建后核对各平台展示名称及 Android applicationId、安装和更新身份的一致性。图标需检查桌面与托盘的小尺寸识别，以及与应用连接状态的区分；实际素材另行交付和验收。当前只有设计文档，尚未创建或验证安装包。

## 关联文档

- [主设计](../superpowers/specs/2026-10-10-sunipip-client-design.md)
- [设计审阅报告](../superpowers/reviews/2026-10-10-sunipip-client-design-review.md)
- [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)
- [实施计划](../superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)

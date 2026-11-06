# 资源筛选栏修复（2026-10-19）

版本：`0.1.0-beta.1+15`。

## 问题与修改

此前“连接资源 / 全部资源”切换控件比搜索和地区框矮；选择“全部资源”后，顶栏的搜索和地区控件被移除，搜索框又出现在内容区，导致布局和切换文字位置变化。

现在顶栏始终保留三个控件，统一为 48px 高、小圆角、细边框。切换项使用背景色表示选中，去掉会挤动文字的勾选图标。显式约束按钮内容高度，让内部着色区域和外框一致，而不是只增大外框。输入控件显式使用标准密度，避免 Windows 默认紧凑密度把搜索框压矮，产生 4px 的垂直偏差。

搜索框在原位供两类资源共用，保留输入内容。“全部资源”继续按已加载资源的名称、编号、状态搜索，通过 `OwnedResourcesView.searchQuery` 接收查询文本，不再渲染第二个搜索框。

资源归属查询的数据结构没有地区字段，因此“全部资源”中的地区框保留原位、显示“全部地区”并置灰，悬停说明暂不支持地区筛选；返回连接列表时恢复此前的地区选择。未从名称猜测地区，也没有增加 API 请求或改变连接资格。

## 验证

- 全量 Flutter 测试：359 项通过。
- 静态分析：无问题。
- 新增 5 项 Windows 平台回归：480、900、1280 像素窗口以及 130% 字体缩放；检查切换中、等待查询及加载完成后三个控件和切换文字的位置不变。桌面密度偏差在修复前有失败复现（`resource-toolbar-windows-before.log`）。
- 检查按钮实际着色区域高度、窄窗口换行、共用搜索的名称/编号/状态过滤、大小写与空白处理、订阅/资产切换、地区恢复和无重复查询。
- 已查看生成的宽/窄窗口对照截图。截图仅使用模拟数据，不进行真实网络接管。

复现（在 `Windows/app` 中）：

```powershell
flutter test --no-pub test/resource_toolbar_test.dart test/resource_ui_regression_test.dart test/owned_resources_view_test.dart
flutter test --no-pub
flutter analyze --no-pub
```

可选截图参数：

```text
--dart-define=BLUEGRASS_TOOLBAR_SCREENSHOTS=E:/workspace/APP/Windows/.artifacts/screenshots/resource-toolbar
```

证据：`Windows/.artifacts/verification/resource-toolbar-full-tests.log`、`resource-toolbar-analyze.log`、`resource-toolbar-release-build.log`；8 张切换前后截图位于 `Windows/.artifacts/screenshots/resource-toolbar/`。

发布包仍使用外层 `BlueGrass.exe` 加 `runtime/` 的结构。打包脚本自动替换 `Windows/dist/BlueGrass-Windows-x64/` 中旧的解压版本。

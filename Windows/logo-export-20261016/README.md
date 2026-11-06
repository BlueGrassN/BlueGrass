# BlueGrass Windows 客户端 Logo 提取

提取日期：2026-10-16；图文间距统一更新：2026-10-19。原始来源：提取时的 Windows 源码及 `Windows/dist/BlueGrass-Windows-x64`。本次调整仅用于本对话的导出素材。

打开 `preview.png` 查看总览；`preview.html` 可直接在浏览器打开。图形、颜色和字号沿用提取版本；全部图文组合已采用本对话确认的紧凑间距。

## 素材目录

| 文件 | 内容 / 用途 |
| --- | --- |
| `original/bluegrass-windows.ico` | Windows 原始应用图标，64×64。桌面、窗口、任务栏和托盘共用。 |
| `original/bluegrass-windows-embedded.ico` | 从当前启动器 / 主程序 EXE 的资源 101 原样重组的 ICO。与源码 ICO 的像素完全一致，文件头元数据略有差别。 |
| `original/*64x64.png` | 两份 ICO 原始分辨率的无损 PNG 导出。 |
| `original/bluegrass-leaves.svg` | 仓库 `app/assets/brand.svg` 原文件。绿色圆角双叶版，未登记到当前 Flutter 资源清单。 |
| `original/bluegrass-leaves-256.png`、`*-1024.png` | 上述 SVG 的 256 / 1024 像素透明 PNG。 |
| `ui/bluegrass-grass-light.svg`、`*-dark.svg` | 从当前发行包 MaterialIcons 字体提取的草叶轮廓，使用界面实际浅色 / 深色主题颜色。 |
| `ui/bluegrass-grass-*-256@1x.png`、`*@4x.png` | 纯草叶图标，256×256 / 1024×1024，透明背景。 |
| `ui/bluegrass-wordmark-*-23@*.png` | 侧栏、窄窗口登录页的草叶加 BlueGrass 文字组合。 |
| `ui/bluegrass-wordmark-*-25@*.png` | 宽窗口登录页的文字组合。 |
| `ui/bluegrass-wordmark-*-28@*.png` | 关于页的文字组合。 |

`light` 用于浅色背景，`dark` 用于深色背景。23 / 25 / 28 是源码中的文字字号；每种组合提供 1× 和 4× PNG。文字组合四周保留 2 个逻辑像素透明边距，避免字形贴边被裁切。

## 来源与核对

- 当前界面不是直接使用 `brand.svg`：`app/lib/main.dart` 的 `_Brand` 组件以 `Icons.grass` 和“BlueGrass”文本绘制，字体为 Microsoft YaHei、字重 800、字距 -0.7，提取时源码的图文间距为 9。本导出包将间距单独统一为 1.75，文字整体左移 7.25 个逻辑像素，与已确认的 SVG 一致；客户端源码保持当前状态。
- 界面草叶浅色主题颜色为 `#087e98`，深色主题颜色为 `#67d2e5`；文字颜色分别为 `#182b3a`、`#e8eef3`。
- 文字 PNG 使用导出目录中的组件副本和主题通过 Flutter 离屏渲染导出，仅调整该副本的图文间距，加载本机微软雅黑常规 / 粗体。纯草叶 SVG 直接提取自发行包 `MaterialIcons-Regular.otf` 的 `U+E2E4` 字形。
- 实际 ICO 为方形青绿色底，仓库 SVG 为绿色圆角底；两者确实不同，均予以保留。ICO 只有 64×64 原始帧，没有将放大图冒充原始高清图。
- 扫描当前便携包中的全部 7 个 EXE，启动器和主程序各有一组相同的品牌图标；Diagnostics、Native、Repair、Service、Update 没有独立图标资源。
- 登录页、侧栏和关于页共用同一图文组件，没有发现额外品牌 Logo。导航按钮、网络示意图和安装器工具链的通用图形未混入品牌素材。
- `source/` 保留原组件片段、主题、原图标生成脚本及本次渲染工具；这些是来源资料，无需运行即可使用图片。没有复制系统字体文件。
- `verification/` 保存 EXE 资源来源、渲染尺寸、完整素材清单和 SHA-256。原始 SVG、ICO 与源码文件逐字节一致。

本次仅更新导出素材、预览、来源说明及素材 ZIP，没有修改或重新打包客户端。

## 新增：dark 图标 + 深色文字组合 SVG

`ui/bluegrass-wordmark-dark-icon.svg` 为用户指定的图文组合：左侧草叶使用 dark 版 `#67D2E5`，右侧文字保留 `#182B3A`。SVG 为透明背景，全部图形和文字都转为路径，不依赖系统字体，不嵌入位图。相邻同名 PNG 为透明预览；根目录 `preview-wordmark-dark-icon.png` 为白底展示图。字体、字距、字号及颜色沿用原版，图文间距采用本对话已确认的缩小版。所有 23 / 25 / 28 字号、浅色 / 深色 PNG 已同步采用该间距。

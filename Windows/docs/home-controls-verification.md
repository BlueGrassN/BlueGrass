# 首页连接区与固定代理端口（2026-10-13）

## 修改

- 本地 HTTP 代理固定监听 `127.0.0.1:12808`。生产入口共用同一端口常量，重连不随机换端口；占用时启动失败，不改用其他端口、不关闭已有监听程序。
- “连接／断开连接”和“选择资源”使用同一按钮样式、尺寸和间距。
- “全局接管”开关移入连接卡片，与两枚按钮同行；窄窗口自动换行。连接期间继续锁定模式切换。
- 只调整原有构造参数和界面布局，没有增加端口设置页面或新的服务层。普通单元测试使用临时端口，生产端口的专门测试验证固定地址、重复启动及占用处理。

## 验证

- `localengine`、`nativehost` 两个 Go 包测试通过；固定端口测试保留了修改前的失败记录。
- 14 项 Flutter 界面测试通过，覆盖按钮尺寸、开关位置与操作、连接期间禁用，以及 480 像素窄窗口布局。
- Flutter 静态分析无问题。
- 真实 Windows 集成测试从生产 API 拉取 5 个节点，逐一连接、检测出口并断开。5 次均监听并显示 `127.0.0.1:12808`，5 次 HTTPS 出口与界面显示一致，5 次均恢复原系统代理设置。
- 已查看真实连接截图 `../.artifacts/screenshots/home-controls/live-connected-0.png`。测试结束后 `12808` 无残留监听，原 `10808` 服务仍由同一进程监听。

证据位于 `.artifacts/verification/home-controls-{port-red,native-test,ui-test,analyze,native-build}.log` 和 `home-controls-live-verbose.log`。首次桌面测试构建返回 MSB8066，未进入测试；相同源码在开启详细构建日志后重新构建并通过，失败日志保留为 `home-controls-live.log`。

## 交付

版本 `0.1.0-beta.1+10` 使用生产入口 `lib/main.dart` 构建。旧解压目录已由构建脚本删除，新包已解压至 `dist/BlueGrass-Windows-x64/`，并已启动客户端。归档校验通过，解压 AOT 与本次 Release 构建一致。

- ZIP：`dist/BlueGrass-Windows-x64-dev.zip`，42,851,377 字节，32 个归档项。
- SHA-256：`379e9f9250b8426ed12ee0a6f9aa896cf85610a80795f565b476497b71c2db1f`。
- 证据：`home-controls-release-build.log`、`home-controls-bundle.json`。

本轮验证系统代理连接及界面布局；未新增全局接管服务的实机验收。包仍未签名。

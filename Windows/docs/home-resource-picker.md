# 首页资源就地选择

日期：2026-11-05。

首页“选择资源”现展开靠近按钮的资源面板，选择后更新首页，不再自动切到“我的 IP”。面板支持按名称、地区、城市、备注或编号搜索；标记当前选择；支持刷新和加载更多；点击外部、关闭按钮或 Esc 可取消。

连接中选择另一资源仍使用原有确认流程；确认后按既有规则暂停/断开，需手动重新连接。已过期或配置不可连接的资源置灰，超过可见保留期限的资源不显示。“我的 IP”与面板共用同一资格提示函数。账号失效会只移除本面板，保留其上方可能存在的到期通知。

[浅色预览](../.artifacts/screenshots/home-resource-picker/1280-1.0-light-resource-picker.png) · [深色预览](../.artifacts/screenshots/home-resource-picker/1280-1.0-dark-resource-picker.png) · [窄窗口预览](../.artifacts/screenshots/home-resource-picker/480-1.3-light-resource-picker.png)。预览均为模拟数据。

## 验证

- 新增 8 项选择行为和生命周期测试：[针对性结果](../.artifacts/home-resource-picker/lifecycle-green.log)。
- 6 组窗口尺寸/字号/主题布局测试通过：[截图验证](../.artifacts/home-resource-picker/screenshots.log)。
- [静态分析](../.artifacts/home-resource-picker/analyze.log)无问题；[完整回归](../.artifacts/home-resource-picker/flutter-tests.log) **392 项全部通过**；[独立审阅](../.artifacts/home-resource-picker/review.md)无未解决问题。
- [构建](../.artifacts/home-resource-picker/build.log)成功，启动器和 runtime 程序版本均核对为 **0.1.0-beta.1+21**；文件大小和 SHA-256 见[发行清单](../.artifacts/home-resource-picker/release-manifest.json)。

## 更新包

当前客户端仍使用原解压目录，本次[构建脚本](../scripts/build.ps1)通过 NoExpand 开关跳过自动替换。新版已解压到独立目录：[运行 build21](../dist/BlueGrass-Windows-x64-build21/BlueGrass.exe)。先从托盘退出旧客户端，再打开这个新版入口。完整便携包为 [BlueGrass-Windows-x64-dev.zip](../dist/BlueGrass-Windows-x64-dev.zip)。

改动前保留了 [build20 便携包](../.artifacts/home-resource-picker/BlueGrass-build20.zip)及[相关源码快照](../.artifacts/home-resource-picker/baseline/)。

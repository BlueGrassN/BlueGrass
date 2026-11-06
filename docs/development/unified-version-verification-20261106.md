# 统一版本与 Release 清理验证（2026-11-06）

公开版本统一为 `0.1.0`，唯一配置为根 `release.json`。以后修改该文件中的 X.X.X 版本，三端构建齐后整体更新固定 preview；已完整发布的版本保持不可变。

## 清理与发布结果

按用户要求删除了 4 个非 preview Release 及其 15 个附件。独立查询 GitHub Release API，确认仅剩 [preview](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)。最初仅移除 Release 和附件，后续补充清理了 4 个对应的 Git 发布标签；再次查询远端与本地标签，均仅剩 preview，并核对 preview 标签指向、说明及 12 个附件未变。README 仍不在 Git 跟踪范围内。

[统一发布运行 42202651000](https://github.com/BlueGrassN/BlueGrass/actions/runs/42202651000) 的测试与发布均成功。页面所有平台均显示 0.1.0，12 个附件的文件名不含 beta、dev、+build 或 attempt；没有未完成 pendingRelease、待清理附件或额外附件。逐个比对 GitHub 摘要、大小以及实际下载字节的 SHA256，全部一致。

最终发布器只读预演返回 `targetVersion: 0.1.0`、`updated: []`、`waiting: []`、`errors: []`。

## 实际安装包检查

- Windows：[构建 42201091040](https://github.com/BlueGrassN/BlueGrass/actions/runs/42201091040)，来源 `ece74e936426057b2a70f450ae75e9a92777ed7f`。ZIP 内启动器和 runtime 客户端的 ProductVersion、FileVersion 字符串均为 `0.1.0`，数字型文件版本均为 `0.1.0.32`，内部 build 为 32。
- Android：[构建 42198492631](https://github.com/BlueGrassN/BlueGrass/actions/runs/42198492631)，来源 `1498d2d6a1f4c7e264f8bb492024e6f3040b7065`。直接用 aapt2 读取 APK，versionName 为 `0.1.0`、versionCode 为 100008，应用 ID 仍为 `com.bluegrass.client`；签名证书指纹与既有 Alpha 一致，构建元数据为 configured/非脏源码。
- macOS：[双架构构建 42198492572](https://github.com/BlueGrassN/BlueGrass/actions/runs/42198492572)，来源 `1498d2d6a1f4c7e264f8bb492024e6f3040b7065`。直接读取两个 ZIP 内 Info.plist，CFBundleShortVersionString 均为 `0.1.0`，CFBundleVersion 均为 `19`，Bundle ID 仍为 `com.sunipip.bluegrass.macos`。

Windows 首次构建在发现 Flutter 会给 PE 字符串附加构建号后主动取消；后续 Windows 专项修正保留系统数字型第四段，仅将公开字符串改为三段，并在生成发行 metadata 前核对实际 EXE。Android/macOS 使用已成功且版本正确的原构建；逐端真实来源如上，未改写旧包的版本元数据。

## 回归与边界

发布器 34 项行为测试、Windows 版本与真实 PE 资源专项 7 项测试通过。覆盖统一版本等待、旧状态迁移、版本/来源/哈希拒绝、回退防护、同版本幂等、未完成事务恢复、更新构建后的同名冲突、无关附件保护、草稿状态及只读预演。独立审阅中的恢复与冲突问题均已用先失败后通过的回归关闭。

共享与三端 Flutter 回归/分析、平台构建工具测试、Actionlint，以及各成功工作流规定的原生与打包检查通过。Windows/macOS 关于页和许可页的实际 widget 回归证明公开显示不含内部构建号，诊断保留兼容的版本/构建信息。

本轮验证版本、打包与发布流程，未新增真实账号联网或物理设备验收。Windows 未签名便携包、Android 既有 Alpha 签名、macOS ad hoc/未公证状态保持原事实。实际脚本、公共版本配置和开发文档已同步原工作目录；本地 README 与原有 `.gitattributes` 修改保留。

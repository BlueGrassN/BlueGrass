# 任务栏空白图标修复（2026-10-15）

## 原因

实际检查确认 `BlueGrass.exe` 内的大、小图标都可正常提取。Windows 当前用户的 `BlueGrass.lnk` 和通知激活注册仍指向已经删除的 `Windows/dist/BlueGrass-Windows-x64-dev/BlueGrass.exe`，而程序已改为从 `Windows/dist/BlueGrass-Windows-x64/BlueGrass.exe` 启动。任务栏依据同一个 AppUserModelID 查到失效快捷方式，因此显示通用文件图标。

## 修改

- 启动时即确保 Shell 注册，不再等待登录后的第一条通知。
- 原快捷方式具有相同应用身份且同名旧程序确实不存在时，更新快捷方式目标、工作目录、通知激活路径和显式图标位置。
- 图标固定引用当前 `BlueGrass.exe,0`，保存后通知 Windows 更新这个快捷方式。
- 保留其他仍存在的安装和不匹配的应用身份；部分更新中断后的旧快捷方式或激活路径也可单独修复。
- 更新快捷方式时使用读写加载模式，避免只读加载后保存失败。

## 验证与交付

`scripts/test-desktop-registration.ps1` 通过：旧路径不存在时修复、显式图标、部分状态修复、保留有效安装、拒绝不匹配注册、临时验证身份清理。测试只使用独立验证身份，不发送通知，不修改生产通知权限。

`0.1.0-beta.1+12` 已构建、校验并替换 `dist/BlueGrass-Windows-x64/`，且已重新启动。实际 Windows 快捷方式和通知激活路径均已指向当前解压目录；目标文件存在，图标位置为当前 exe 的第 0 个图标，AppUserModelID 仍为 `com.bluegrass.client`。没有重启 Explorer 或清空全系统图标缓存。

日志和结果位于 `.artifacts/verification/taskbar-icon-registration-test.log`、`taskbar-icon-registration.json`、`taskbar-icon-release-build.log` 与 `taskbar-icon-bundle.json`。

ZIP 为 42,951,054 字节，SHA-256 为 `279371e1c64f67de9b866728d889582ebaaadfcd1f053a5e5f177fb73052ba9d`。

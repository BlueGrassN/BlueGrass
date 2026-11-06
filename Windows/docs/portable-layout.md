# Windows 便携包目录整理（2026-10-15）

版本：`0.1.0-beta.1+14`。解压目录固定为：

```text
Windows/dist/BlueGrass-Windows-x64/
├── BlueGrass.exe
└── runtime/
    ├── BlueGrass.exe
    ├── BlueGrass.Native.exe
    ├── BlueGrass.Diagnostics.exe
    ├── BlueGrass.Update.exe
    ├── BlueGrass.Service.exe
    ├── BlueGrass.Repair.exe
    ├── flutter_windows.dll
    ├── *.dll
    ├── LICENSE.sing-box
    └── data/
```

日常使用只需运行外层 `BlueGrass.exe`。复制或移动客户端时保留整个文件夹。

## 实现

外层入口静态链接 C++ 运行库，只依赖 Windows 的 `KERNEL32.dll` 和 `USER32.dll`，带有应用图标和同一版本号。它根据自己的位置启动 `runtime/BlueGrass.exe`，将工作目录设为 `runtime`，原样转发参数，然后退出。没有控制台、后台常驻启动器或 PATH/DLL 搜索配置。

实际客户端、辅助程序、DLL 和数据保持相邻，所以现有进程通信、资源加载及辅助程序路径继续成立。快捷方式和自启动使用外层入口；通知 COM 激活直接指向内层实际客户端，避免依赖启动器的存活时间。旧扁平目录的注册项按同一安装位置迁移，兼容 Windows 路径大小写，仍保留其他有效安装的注册项。

`stage-portable.ps1` 复用 `installer/payload.ps1` 的运行文件清单，仅复制必要载荷。`.lib`、调试符号和未列入清单的旧构建残留不进入 ZIP。Flutter 资源清单引用的 `LICENSE.sing-box` 与根许可文件都保留。

`build.ps1` 将暂存目录压缩后调用 `expand-bundle.ps1`。仍然先删除旧的固定解压目录，再解压新包；旧界面备份位于 `Windows/backups/`，不受影响。编译输出目录和签名安装器的受保护安装布局保持原有形式，本次整理针对便携 ZIP。

## 验证与证据

```powershell
./Windows/scripts/test-portable.ps1
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 14
python ./Windows/scripts/verify-release-bundles.py
python ./Windows/scripts/verify-portable-runtime.py
```

- 启动器测试：中文与空格路径、从其他工作目录启动、空参数、引号、尾部反斜杠、自启动和通知参数原样转发，启动器无运行库 DLL 依赖。
- 注册测试：新旧布局的自启动、快捷方式图标、通知 COM 注册、旧路径大小写迁移、重复执行和其他安装保护。使用隔离测试标识，清除测试注册项，不发送通知。
- 包校验：根目录严格只有主程序和 `runtime`，检查实际发布入口的 PE 导入表、原生载荷哈希、生产 API 与共享配置地址，并排除编译中间文件和测试私密文件。
- 辅助程序检查：从系统目录启动包内 Native、Diagnostics 和 Update；验证 Native 握手与固定端口释放、无配置诊断报告、更新组件保留既有 `UPDATE_UNCONFIGURED` 边界；前后核对系统代理设置不变。
- 实机检查：从外层入口启动，确认实际窗口和资源列表，核对快捷方式图标及通知激活路径。

日志与机器可读结果保存在 `Windows/.artifacts/verification/portable-*`。本次没有修改 Dart 业务或网络协议；没有重新执行生产节点连接、签名更新安装或 TUN 提权验收。

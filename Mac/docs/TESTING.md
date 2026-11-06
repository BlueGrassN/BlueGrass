# BlueGrass macOS 开发测试包

此包供用户在 Mac 上验收，使用临时本地签名（ad hoc），没有 Apple Developer ID 签名、公证或 App Store 审核。GitHub 构建成功不代表已通过 Mac 实机联网测试。

## 安装

1. Apple Silicon（M 系列）下载 `arm64` 应用 ZIP；Intel 下载 `x86_64` 应用 ZIP。要求 macOS 13 或更高版本。Release 页面可直接下载应用 ZIP；若从 Actions 下载 artifact，先解压外层 artifact。
2. 从 [统一开发预览版](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview) 同时下载本版本前缀的 `*-SHA256SUMS.txt`，运行 `shasum -a 256 BlueGrass-macOS-*.zip`，将结果与其中对应文件名的 SHA-256 核对。
3. 解压应用 ZIP，将 `BlueGrass.app` 放到“应用程序”。退出旧版并主动断开连接后再替换。
4. 首次打开时，开发签名可能触发 Gatekeeper 拦截。仅在确认此包来自自己的本次 GitHub 构建后，按 macOS“系统设置 → 隐私与安全性”给该应用单独允许打开。无需关闭系统级安全功能。
5. 若系统提示访问 BlueGrass 钥匙串记录或修改网络设置，请核对应用名称后授权。拒绝权限时应显示失败，不应报告已连接。

## 本次可测试

- 与 Windows 共用的登录、首页、我的 IP、设置、诊断和关于页面；浅色、深色及跟随系统；宽窗口与窄窗口。
- 实际 API 登录、资源列表与详情、刷新、选择有效资源。
- 系统代理连接与断开、实际出口 IPv4、上传/下载流量、全局/绕过中国大陆分流。
- TUN 开关、系统管理员密码授权、原生 utun 的 IPv4 TCP/UDP、业务 DNS、IPv6 阻断与局域网例外。
- 设置与会话恢复、启动登录项、菜单栏操作和关闭窗口后继续运行。
- 系统代理只覆盖遵循 macOS 系统代理设置的应用，不能据此声称接管所有 TCP/UDP 或提供 VPN 级保护。

TUN 使用按需启动的管理员原生组件，无需 Apple Developer 证书。首次连接会请求系统管理员授权；再次启动组件也可能再次请求。请在系统窗口输入密码，客户端不读取或保存密码。开发包采用手动替换更新，不运行 Windows 更新程序。

## 建议的验收顺序

1. 打开“系统设置 → 网络 → 当前网络 → 详细信息 → 代理”，记录测试前的代理/PAC/绕过设置。
2. 首次启动时点击账号和密码框，确认出现光标且可输入；启动或网络恢复检查未完成时可以先填写，但登录按钮和回车不得提前提交。准备完成后登录、检查资源列表和平台文案，切换主题、调整窗口宽度，确认界面与 Windows 版一致。
3. 连接一条有效线路；若需授权，允许 BlueGrass 修改系统代理。检查客户端实际出口并用遵循系统代理的浏览器联网。
4. 主动断开，核对原有代理设置恢复，其他程序后来修改的设置不应被覆盖。
5. 重新连接后关闭主窗口，从菜单栏恢复窗口；再从菜单栏退出，核对代理恢复。
6. 验证错误密码、无可用资源、权限拒绝、网络中断、切换 Wi-Fi、睡眠唤醒等情况，记录真实现象。不要将系统代理的覆盖范围当作全局 TUN。
7. 检查诊断导出不含登录密码、账号 token 或节点密码。

## TUN 验收

先完成系统代理验收并主动断开，再开启同一个界面中的“TUN 模式”。

1. 点击连接并完成系统管理员授权。确认真实出口和流量，再使用不依赖系统代理的程序测试 IPv4 TCP、UDP 和 DNS。分别测试全局/绕过中国大陆模式、局域网开关；公网 IPv6 应被阻断。
2. 保持 TUN 连接，刷新资源和账号信息，确认 API 仍可访问，主题、布局和导航与 Windows 一致。
3. 主动断开后核对原来的 DNS、路由和联网恢复。系统代理与 TUN 之间只在断开后切换。
4. 再次连接时分别测试取消授权、授权超时、错误密码；客户端不得显示已连接。取消后若仍提示恢复未确认，按“断开连接”完成一次授权恢复。
5. 测试切换 Wi-Fi、断网重连和睡眠唤醒：连接应暂停或要求重连，不应显示过期的出口。暂停及故障时保留网络保护；“断开连接”才释放保护并恢复本应用拥有的网络设置。
6. 在可中断网络的测试时段，测试强制结束客户端/原生组件。重新打开应用、授权恢复，再点击断开连接。崩溃后 PF 保护可能继续阻止公网；不要通过删除日志、全局清空 PF 或关闭其他防火墙来恢复。
7. 对比正常退出、菜单栏退出、取消授权和崩溃恢复后的 DNS/路由。其他应用后来修改的设置应被保留；冲突应明确显示恢复未确认。

TUN 的受保护状态位于 `/Library/Application Support/BlueGrass/Tunnel`，可跨重启发现的恢复标记为 `/Library/Application Support/BlueGrass/tun-intent.json`，运行时通信位于 `/Library/Application Support/BlueGrass/Run`。测试期间保留这些目录及安装的组件。替换旧版或移除应用前，请先在应用中断开并确认网络恢复。当前无常驻开机 LaunchDaemon；系统授权对话框和完整全局接管仍需由你在实际 Mac 验收。

反馈请附：macOS 版本、芯片类型、`BUILD.json` 中 commit/build、操作步骤和错误码。勿提供账号密码或节点密钥。崩溃/强制结束后的网络恢复、不同网络服务与权限状态均属于需实机确认的项目。

## 构建方式

向 main 推送根 release.json、Mac 或共享依赖会触发 macOS development build，也可在 Actions 手动运行。公开版本从根 release.json 读取，显示和包名只用 X.X.X，例如 `BlueGrass-macOS-0.1.0-arm64.zip`；内部构建号及来源保留在 BUILD.json。两个架构须同一次成功运行，且 Windows/Android 相同版本均就绪后，由 publish-preview.yml 整体更新固定 [preview Release](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)。任一平台失败保留上一整组下载。同版本文件不可覆盖，新发行先递增根版本。artifact 保留 14 天。构建、并发与恢复见 [统一发布手册](../../docs/development/github-releases.md)；Mac/tools/build.py 也可在配置同版本 Flutter/Go/Xcode 的 Mac 上运行。

统一发布器读取 main 分支运行列表，再逐条核验成功状态与来源，避免 GitHub 状态筛选索引滞后漏掉新包。以 preview 页面逐端 commit/run 和附件摘要确认更新；补发使用共享发布手册中的统一工作流。

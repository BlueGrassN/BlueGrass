# BlueGrass macOS

macOS 客户端的开发入口。当前开发方式为 Windows 编写与测试共享代码，GitHub Actions 在 macOS 上构建，由用户将产物拿到 Mac 验收。

`app/` 是导入现有 Windows 桌面 UI 的薄 Flutter 应用：页面、导航、主题和组件共用同一份代码。Swift 宿主提供 Keychain、菜单栏、登录项和本地窗口能力；代理协议继续使用固定版本的 Go/Xray。

开发包提供系统代理，TUN 尚未提供签名的 Network Extension，因此明确禁用。产物使用 ad-hoc 开发签名，未公证；不代表正式发行或实机联网验收完成。

- [Mac 安装与测试说明](docs/TESTING.md)
- [GitHub 构建流程](../.github/workflows/macos-dev.yml)：Apple Silicon/Intel 分别构建、验证和上传 ZIP。
- [本地 Mac 构建脚本](tools/build.py)：`python3 Mac/tools/build.py --arch arm64 --build-number 1`，Intel 将架构改为 `x86_64`。

- [macOS 开发手册](../docs/development/macos.md)：原生 Network Extension Packet Tunnel、共享 Go/Xray 桥接、平台保护与验证要求。
- [四端共享开发手册](../docs/development/four-platform-development.md)：统一版本、API、远程配置、状态与证据要求。
- [ADR 0049：四端仅使用 Xray-core](../docs/adr/0049-xray-only-four-platforms.md)与[协议矩阵](../docs/development/xray-protocol-matrix.md)。

唯一代理核心为 Xray-core v26.10.17（上游预发布），禁止 sing-box 回退或第二代理内核。默认 API `https://client-api.sunip.vip/api/client/v1`、官网 `https://sunipip.com/`、远程配置 `https://client-api.sunip.vip/client-config/config.json`；这些是待实现的统一要求，不是接入完成声明。

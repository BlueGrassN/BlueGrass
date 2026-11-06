# BlueGrass macOS

下载统一使用 [开发预览版 Release](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)，各端安装包集中展示并独立更新；规则见 [共享发布手册](../docs/development/github-releases.md)。

macOS 客户端的开发入口。当前开发方式为 Windows 编写与测试共享代码，GitHub Actions 在 macOS 上构建，由用户将产物拿到 Mac 验收。

`app/` 是导入根 `packages/client` 公共客户端库的薄 Flutter 应用：页面、导航、主题和组件共用同一份代码。Swift 宿主提供 Keychain、菜单栏、登录项和本地窗口能力；代理协议继续使用固定版本的 Go/Xray。

开发包提供系统代理和管理员密码授权的 TUN。TUN 通过系统授权窗口按需启动受保护的原生 utun 组件，密码不会进入应用。产物使用 ad-hoc 开发签名，未公证；实际 Mac 联网验收由用户完成。此实现不依赖 Apple Developer 证书，也不使用 Network Extension。

- [Mac 安装与测试说明](docs/TESTING.md)
- [GitHub 构建流程](../.github/workflows/macos-dev.yml)：Apple Silicon/Intel 分别构建、验证和上传 ZIP。
- [本地 Mac 构建脚本](tools/build.py)：`python3 Mac/tools/build.py --arch arm64 --build-number 1`，Intel 将架构改为 `x86_64`。

- [macOS 开发手册](../docs/development/macos.md)：管理员 TUN、共享 Go/Xray、平台保护与后续签名分发要求。
- [TUN 平台](docs/tun-platform.md)与[授权组件](docs/tun-authorization.md)：实现、生命周期和测试命令。
- [四端共享开发手册](../docs/development/four-platform-development.md)：统一版本、API、远程配置、状态与证据要求。
- [ADR 0049：四端仅使用 Xray-core](../docs/adr/0049-xray-only-four-platforms.md)与[协议矩阵](../docs/development/xray-protocol-matrix.md)。

唯一代理核心为 Xray-core v26.10.17（上游预发布），禁止 sing-box 回退或第二代理内核。默认 API `https://client-api.sunip.vip/api/client/v1`、官网 `https://sunipip.com/`、远程配置 `https://client-api.sunip.vip/client-config/config.json`；这些是待实现的统一要求，不是接入完成声明。

# BlueGrass iOS

iOS 客户端的开发入口。2026-10-19 盘点时尚无应用或 Xcode 工程，本目录当前仅有开发文档；没有可运行客户端、签名包、真机 VPN 或商店接受结果。

- [iOS 开发手册](../docs/development/ios.md)：原生 Network Extension Packet Tunnel、共享 Go/Xray 桥接、平台生命周期与验证要求。
- [四端共享开发手册](../docs/development/four-platform-development.md)：统一版本、API、远程配置、状态与证据要求。
- [ADR 0049：四端仅使用 Xray-core](../docs/adr/0049-xray-only-four-platforms.md)与[协议矩阵](../docs/development/xray-protocol-matrix.md)。

唯一代理核心为 Xray-core v26.10.17（上游预发布），禁止 sing-box 回退或第二代理内核。默认 API `https://client-api.sunip.vip/api/client/v1`、官网 `https://sunipip.com/`、远程配置 `https://client-api.sunip.vip/client-config/config.json`；这些是待实现的统一要求，不是接入完成声明。

# API 域名迁移与四端共享配置（2026-10-15）

默认 API 改为 `https://client-api.sunip.vip/api/client/v1`，同步覆盖 Dart、原生 HTTPS 的默认 Host/SNI/证书名称、服务默认域名、诊断及构建和实测脚本。

部署与后续操作统一见 [四端共享配置操作手册](../../client-config/README.md)，包含静态文件发布、Nginx 精确路径、响应验证、切换、回退和完整退出客户端的方法。

## 共享配置

独立目录为仓库根目录 `client-config/`；公开地址固定为 `https://client-api.sunip.vip/client-config/config.json`。该文件没有平台前缀，四端共用同一 JSON 协议。本轮实现 Windows 消费端，其他端代码未修改。

Windows 在网络恢复检查之后、恢复账号之前读取一次配置。普通启动的下载上限为 5 秒、4 KiB，不发送登录凭据，也不跟随重定向。仍存在 TUN 保护时只使用缓存，不通过普通 DNS/HTTP 绕开保护。配置缓存使用现有 DPAPI 记录；非法配置、404、网络错误或较旧 revision 不覆盖有效缓存。没有缓存时使用默认 `.vip` 地址。

`revision` 必须单调递增；同 revision 不允许改变地址，主动回滚域名也要增加 revision。只接受规范 HTTPS 域名、443 端口和现有 `/api/client/v1` 路径。下一次进程启动生效，当前连接不热切换。

## 连接和身份边界

- Dart 传输只接受当前选定 API origin；原生 Flutter bridge 在启动时固定 TLS 主机，开始请求后不能更换。每次请求仍不能自行传入 Host，HTTP Header、TLS SNI 与证书验证使用同一固定主机。
- 服务对经过现有认证的 GUI 请求执行严格 DNS 主机名检查、公共 IPv4 检查和原有物理 DNS 路径约束。为支持远程域名，DNS 解析不再限于单个编译主机名；实际 API origin 的选择和 TLS 身份由 GUI 启动配置和原生 HTTPS pin 负责。未扩大 WFP 网络规则，未接受 URL、端口或证书绕过参数。
- API 环境仍以完整地址区分。域名变化需要显式重新登录，旧域名的 access/refresh token 不会发送到新域名。原有环境记录不会被静默迁移或销毁。

## 验证

- Flutter 全量 348 项测试通过，静态分析无问题。
- 新增共享配置、匿名固定来源、缓存、404/重定向/超限、revision、保护状态及 URL 校验；更新了传输 origin 固定和旧会话隔离回归。
- 4 项登录与环境隔离测试通过；`winservice`、`nativehost` 包测试通过。
- C++ HTTP、证书、远程主机名称匹配与旧域名证书拒绝测试通过。
- 原生物理 HTTPS 实测新域名健康接口返回 200；真实 Flutter method channel 验证启动配置固定、二次改主机被拒绝及 HTTPS 健康请求。
- 真实新 API 登录与分页界面测试通过：5 个节点（3 页）、8 条订阅（4 页）、2 条备注；界面显示 6 条订阅，隐藏超过 72 小时的 2 条。

日志位于 `.artifacts/verification/api-migration-*`。首次桌面构建出现 MSB6006（Lib.exe），相同源码重新构建后通过；首次同时测试时出现本地端口监听冲突，独立重跑 nativehost 通过。原始失败日志保留。

当前线上共享配置路径返回 404，配置文件尚未发布；默认新 API 已可用。生产上尚未执行远程指向第二个真实域名的切换，相关逻辑由配置、TLS 与原生桥接测试覆盖。本轮没有安装或重新验收特权 TUN 服务，也没有修改生产 API 程序。

## 打包交付

`0.1.0-beta.1+11` 已从 `lib/main.dart` 构建，旧解压目录已替换为 `dist/BlueGrass-Windows-x64/` 的新内容。归档校验确认新 API 域名与四端共享配置地址已编译入程序，不含旧域名、测试账号文件或测试绑定；解压 AOT 与 Release 构建一致。

ZIP 为 42,945,953 字节、32 个归档项，SHA-256 为 `a1a5259613768bb393f196b32dea2d710ef3bf0e1e5381bd27bc8db06a056cf7`。日志：`api-migration-release-build.log`、`api-migration-bundle.json`。共享配置独立交付于仓库根目录，不放入 Windows 专用目录，也不随打包自动发布到生产服务器。

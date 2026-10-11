# BlueGrass 独立客户端 API

> 历史 PHP 方案，已被用户要求的 [Go API 设计](2026-10-11-client-api-go-design.md)替代，不再用于实施。当前接口及客户端接入见[开发文档](../../development/client-api-integration.md)；下文保留原方案记录。

用户已授权在 Hytron 开发和放置代码，代码保留于 APP；官网源码只读，禁止连接官网生产服务器、修改生产数据、Git 提交或推送。用户已自行在宝塔创建网站；站点、Nginx、SSL、证书和服务重载由用户管理，本任务不操作。

## 目标与环境

- 独立服务 `client-api/`，独立官网登录和 Sanctum token。
- Hytron 82.152.166.237，宝塔 PHP 8.2.34、Nginx、MySQL；域名 test-api.dos.im。
- Laravel 12 / PHP 8.2，沿用生产技术栈。11 已于 2026-03-12 结束安全支持；12 兼容 PHP 8.2。
- 客户及订阅来源是本机测试库 suntest。源码只 SELECT，MySQL 会话设置 TRANSACTION READ ONLY；无上游 HTTP/SSH、无官网业务模型/事件/任务。
- 会话使用服务独立 SQLite（WAL、10 秒 busy timeout、IMMEDIATE transaction），数据和 APP_KEY 独立持久化，不使用官网 personal_access_tokens，不修改 customers.last_login_*。
- 密码仅用于首次校验，使用 PHP password_verify 兼容数据库已有 bcrypt/Argon2；不保存、不重写密码。

## 会话契约

- POST /api/client/v1/auth/login：username、password、device_id(UUID)、device_name(可选)。同客户同设备重新登录撤销该设备旧会话；不同设备独立。
- 30 分钟 access token；refresh token 180 天闲置过期，成功续期滑动延长，无固定会话总期限。
- 使用独立前缀的随机 256-bit 不透明 token；数据库仅保留 SHA-256 摘要。每次鉴权检查会话、账号启用/删除状态及密码指纹；改密、禁用使旧会话失效。
- POST /auth/refresh：refresh_token、request_id(UUID)。事务内轮换 token，旧 token 保留用于识别重放。
- 同一个 refresh_token + request_id 在 120 秒内重试返回同一结果（加密缓存）。仅当该轮仍是当前轮时返回缓存；已推进返回 409 stale_refresh，不回退令牌。
- 不同 request_id 重用已消费 token 撤销该设备会话。客户端必须串行刷新、先持久化 request_id，再请求并原子保存返回值；网络失败保留会话。不得将接口请求超时视为登出。
- POST /auth/logout 撤销整个当前设备会话；GET /me 返回必要客户资料。
- 所有接口 HTTPS、no-store；登录限流按 IP 和账号，刷新限流按 IP；错误结构固定，不包含 SQL、凭据或堆栈。
- 定时清理过期重试密文和超过保留期的失效会话；仅操作独立会话库。

## 节点契约

- GET /nodes?page=1&per_page=100；GET /nodes/{subscriptionId}。
- 归属来自认证 customer_id，不接受请求指定客户。
- 继承 APP 已确认规则：只发有效中转，不回退原始静态 IP。订阅 active 且未到期，IP 未删除/释放，中转 active、has_forward、匹配 proxy_ip_id、设备组启用，端口和主机有效。
- 主机优先级：订阅 custom_host、套餐 display_host、设备组 custom_connect_host、original_connect_host；无入口则不可连接。
- 节点字段：id、name、protocol=socks5、host、port、username、password、country_code、country_name、city、expires_at、is_forwarded；凭据只对本人发放。
- 名称：客户备注、资产名、国家名依次回退，附订阅 ID；时间从来源 Asia/Shanghai 转成 UTC ISO 8601。服务响应包含 server_time。
- 列表只包含可连接节点，分页总数与筛选一致；他人/不存在节点 404，本人失效节点 409 node_unavailable。订阅到期不登出用户。

## 验证和边界

自动测试只用临时 SQLite 来源 fixtures，不改测试库的客户/节点数据；测试独立会话库迁移、登录、续期跨重启、过期/禁用/改密/重放/重试、同设备与多设备隔离、节点归属与中转规则、HTTPS 和错误响应。Hytron 实际验证来源 SELECT、只读会话配置、完整测试集和依赖审计。不探测节点连通性，不调用生产/上游，不创建真实客户。代码放入宝塔现有网站根目录；不修改站点或 SSL 配置。

首版交付密码登录；独立短信登录涉及另行配置短信供应商，不从官网复用发码/校验链路。保留官网原有短信功能，客户端 API 文档明确本版范围。

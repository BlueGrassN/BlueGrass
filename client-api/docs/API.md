# Go Client API v1

Base path `/api/client/v1`，公网使用 HTTPS。成功：`{"success":true,"data":{},"server_time":"...Z"}`；失败：`{"success":false,"error":{"code":"...","message":"..."},"server_time":"...Z"}`。所有响应禁止缓存。

## 接口

| 方法与路径 | 输入/用途 |
| --- | --- |
| POST /auth/login | username、password、device_id(UUID)、可选device_name；用户名或手机号 |
| POST /auth/refresh | refresh_token、request_id(UUID)；不需要access token |
| GET /me | 当前客户资料 |
| POST /auth/logout | 撤销当前设备会话 |
| GET /nodes?per_page=100&cursor=... | 本人可连接节点，游标分页 |
| GET /nodes/{subscriptionId} | 重新获取本人指定资源的最新连接参数 |
| GET /health | 存活检查，不暴露数据库信息 |

受保护接口使用 `Authorization: Bearer <access_token>`，不接受 query token 或官网登录Token。请求JSON最多4096字节，未知字段和尾随文档被拒绝。

## 会话

登录/续期返回 `access_token`、`refresh_token`、`token_type=Bearer`、`expires_in`、`access_expires_at`、`refresh_expires_at`、`session_id`、`customer`。customer只有id、username、display_name。默认访问令牌30分钟，刷新令牌180天闲置到期，成功续期滑动延长，无固定会话总寿命。

客户端持久化device_id和刷新令牌到系统安全存储，不保存用户密码。同账号同设备重新登录会撤销其旧会话。

自动续期必须串行化：发请求前保存本轮request_id，超时/断网/503用同一令牌与同一request_id重试；成功后原子保存新令牌。默认120秒内同请求返回同一结果。已经发生后续续期则返回409 stale_refresh，避免回滚；不同request_id重用旧令牌会撤销该设备。

若恢复窗口过期且新令牌从未保存，需要重新登录；如果已保存新令牌则继续使用它。暂时故障和限流不能清除本地会话。已知改密、停用、删除、会话撤销按明确错误处理。

## 节点与分页

data含 `items` 和 `pagination:{per_page,has_more,next_cursor}`。per_page默认100、最大100。首次不传cursor；之后原样使用next_cursor，直到has_more=false。短页甚至空页仍可能has_more=true（源数据含不可用入口），不能自行认为结束。**Go版不再提供page/total/last_page，也不允许大OFFSET分页**。

节点字段：id（订阅ID字符串）、name、protocol=socks5、host、port、username、password、country_code、country_name、city、expires_at（UTC或null）、is_forwarded=true。只发有效中转；无中转资源不会回退原始静态IP。某页缺少记录不能代替单条资源重新确认。

## 错误

- 401 invalid_credentials：账号或密码错误。
- 401 invalid_token/access_token_expired：使用刷新令牌续期后重试。
- 401 credentials_changed/account_unavailable/session_revoked/session_expired/invalid_refresh_token/refresh_reuse_detected：需要重新登录。
- 403 account_disabled：账号已停用。
- 404 not_found：不存在或不属于本人。
- 409 node_unavailable：资源当前不可连接，不退出账号。
- 409 stale_refresh/refresh_retry_expired：按续期恢复规则处理。
- 422 validation_failed：参数错误；413请求体过大。
- 403 https_required：必须HTTPS；与同为403的account_disabled按code区分。
- 429 rate_limited：遵守Retry-After。
- 503 service_busy/service_unavailable：排队超时、过载或依赖暂时故障；遵守Retry-After并退避重试，保留会话。

本版是密码登录，不接入短信供应商、注册、购买、续费或生产管理功能。

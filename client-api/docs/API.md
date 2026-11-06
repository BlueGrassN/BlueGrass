# Go Client API v1

Base path `/api/client/v1`，公网使用 HTTPS。成功：`{"success":true,"data":{},"server_time":"...Z"}`；失败：`{"success":false,"error":{"code":"...","message":"..."},"server_time":"...Z"}`。所有响应禁止缓存。

2026-10-12 本地实现新增资源查询与显式空出口字段，**本次没有部署服务器**。既有测试站点是否支持新接口须通过 `/capabilities` 核对；404 表示旧接口版本，不能解释为账号没有资源。来源证据、完整性边界和客户端用法见 [资源契约](RESOURCE_CONTRACT.md)。

同日归属查询补全了可由源代码判定的空分配、同客户重叠订阅、交付身份与裸资产历史抑制规则；具体选择矩阵和仍需决定的跨客户共享冲突见 [当前归属映射](resource-ownership-mapping.md)。`snapshot_complete` 仍为 false；本地查询修正不代表服务器已经部署。

## 接口

| 方法与路径 | 输入/用途 |
| --- | --- |
| POST /auth/login | username、password、device_id(UUID)、可选device_name；用户名或手机号 |
| POST /auth/refresh | refresh_token、request_id(UUID)；不需要access token |
| GET /me | 当前客户资料 |
| POST /auth/logout | 撤销当前设备会话 |
| GET /nodes?per_page=100&cursor=... | 本人可连接节点，游标分页 |
| GET /nodes/{subscriptionId} | 重新获取本人指定资源的最新连接参数 |
| GET /capabilities | 当前实现的查询能力及来源完整性限制 |
| GET /resources?kind=subscription&per_page=100&cursor=... | 本人资源摘要；kind 可为 subscription 或 asset，分别分页 |
| GET /resources/expiry?kind=subscription&per_page=100&cursor=... | 同一无凭据摘要投影，供独立到期同步使用 |
| GET /resources/{kind}/{id} | 当前归属的指定类型资源摘要；通用404不证明具体回收原因 |
| GET /health | 存活检查，不暴露数据库信息 |

受保护接口使用 `Authorization: Bearer <access_token>`，不接受 query token 或官网登录Token。请求JSON最多4096字节，未知字段和尾随文档被拒绝。

`client-api.sunip.vip` 的 EO 配置直接读取 `EO-Connecting-IP`，缺失或为空时使用实际连接 IP；Nginx 将选定地址用于访问日志、IP 限流及传给 Go 的 `X-Real-IP`，新登录会话据此记录 `login_ip`。客户端不需要额外请求头。此简化方式不校验 EO 来源，直连源站时伪造此头会影响 IP 记录与限流。配置见 [EO 接入说明](EDGEONE-CLIENT-API-SUNIP-VIP.md)。

## 会话

登录/续期返回 `access_token`、`refresh_token`、`token_type=Bearer`、`expires_in`、`access_expires_at`、`refresh_expires_at`、`session_id`、`customer`。customer只有id、username、display_name。默认访问令牌30分钟，刷新令牌180天闲置到期，成功续期滑动延长，无固定会话总寿命。

客户端持久化device_id和刷新令牌到系统安全存储，不保存用户密码。同账号同设备重新登录会撤销其旧会话。

2026-10-12 本地实现增加成功登录 IP：服务端从连接及可信代理获取 IP，存入会话表 `login_ip`，兼容 IPv4/IPv6。客户端请求和响应字段不变，不允许 JSON 提交 `login_ip` / `client_ip`。续期和节点请求不改写该值，也不将会话绑定到 IP；网络切换无需因此重新登录。新程序部署前须按[部署手册](DEPLOYMENT.md)第 5.3 节准备字段；本次未更新 Hytron 服务。

自动续期必须串行化：发请求前保存本轮request_id，超时/断网/503用同一令牌与同一request_id重试；成功后原子保存新令牌。默认120秒内同请求返回同一结果。已经发生后续续期则返回409 stale_refresh，避免回滚；不同request_id重用旧令牌会撤销该设备。

若恢复窗口过期且新令牌从未保存，需要重新登录；如果已保存新令牌则继续使用它。暂时故障和限流不能清除本地会话。已知改密、停用、删除、会话撤销按明确错误处理。

## 节点与分页

data含 `items` 和 `pagination:{per_page,has_more,next_cursor}`。per_page默认100、最大100。首次不传cursor；之后原样使用next_cursor，直到has_more=false。短页甚至空页仍可能has_more=true（源数据含不可用入口），不能自行认为结束。**Go版不再提供page/total/last_page，也不允许大OFFSET分页**。

节点字段：id（订阅ID字符串）、name、customer_remark、protocol=socks5、host、port、username、password、country_code、country_name、city、expires_at（UTC或null）、is_forwarded=true、expected_exit_ip（当前为null）。只发有效中转；无中转资源不会回退原始静态IP。某页缺少记录不能代替单条资源重新确认。

`name` 表示资产名称 `proxy_ips.asset_name`（去除首尾空白），不使用客户备注替换。真实资产名称直接返回，例如 `美国-198.87.96.137`，不追加 ` #订阅ID` 或 ` #资产ID`；名称可以重复，身份仍由 `id`（资源摘要还需 `kind`）区分。这一语义适用于节点列表/详情、资源列表/详情及到期摘要。

`customer_remark` 是独立的字符串字段，来自当前客户订阅的 `subscriptions.customer_remark`，去除首尾空白；NULL、空字符串或纯空白统一返回 `""`，字段不省略。待交付/待审核订阅也可以返回自己的客户备注。`kind=asset` 的裸资产没有对应订阅，返回 `""`，不借用历史或其他客户的订阅备注。`subscriptions.remark` 和 `proxy_ips.remark` 是内部备注，不通过此字段返回。旧客户端可忽略该新增字段；兼容旧服务器时，字段缺失按无备注处理。

资产名称为 NULL、空字符串或纯空白时，节点依次回退为 `国家名称 #订阅ID`、`Node #订阅ID`；资源摘要回退为 `Resource #ID`。待交付/待审核订阅仍隐藏未经确认的资产名称并使用该中性占位名称。名称不从入口地址、SOCKS 连接串或探测结果推断，名称中的 IP 文本也不构成 `expected_exit_ip` 的权威来源。

[D-29](../../docs/adr/0029-fixed-expected-exit-ip.md) 要求单值 `expected_exit_ip`，供客户端与实际出口严格比对；[D-33](../../docs/adr/0033-current-ipv4-egress.md) 确认当前资源采用固定 IPv4 出口。本地 DTO 已增加此字段，但源码核对未找到权威出口映射，因此返回显式 `null`。不得把入口 `host`、`proxy_ips.ip_address` 或一次探测结果当作预期出口；缺失值必须继续阻止连接资格通过。旧服务器可缺少此字段，Dart 两种情况均映射为 null。

[D-34](../../docs/adr/0034-current-resources-tcp-udp.md) 已确认当前全部资源支持 TCP 与 UDP；本接口仍未提供 UDP 能力字段，实际协议配置与转发须独立验证，不将业务确认写成接口已返回或检测已通过。

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
- 501 capability_unavailable：当前来源未实现资源摘要扩展，不能作为空资源页。
- 503 service_busy/service_unavailable：排队超时、过载或依赖暂时故障；遵守Retry-After并退避重试，保留会话。

本版是密码登录，不接入短信供应商、注册、购买、续费或生产管理功能。

后续短信方向见 [D-26](../../docs/adr/0026-reuse-existing-sms-provider.md)：沿用官网现有供应商、签名及模板，由独立 Go API 发码和校验。短信端点与验证码规则尚未实现，上述当前接口清单没有因此增加短信能力。

[D-27](../../docs/adr/0027-sms-limits-without-captcha.md) 已确定短信采用发送频率/数量和验证码错误次数限制，不接入图形/滑块验证；具体限额及请求响应仍待实现。

# BlueGrass Go API：生产结构兼容及并发边界

用户要求用 Go 替换 PHP，并特别处理大量用户同时登录时的数据库读取。只开发和放置代码；不管理宝塔/Nginx/SSL，不提交或推送；sunipip-dash 仅读，不连接官网生产服务器/数据库。

## 来源与兼容

本地生产代码仍是账号/订阅行为的依据。customers.username/phone 登录，password 是 PHP bcrypt/Argon2；status=1 且 deleted_at=NULL 才能使用。账号密码哈希指纹变化使客户端会话失效，不重写客户密码、last_login 或官网 Sanctum token。

已核对 AuthController.php:145、EnsureCustomer.php:19、Subscription.php:59/95、ForwardRule.php:125、RouterWifiAccount.php:94。保留客户订阅归属、active 及期限、SOCKS5 类型、主机覆盖优先级。历史 released_at 必须与 COALESCE(last_renewed_at,started_at) 比较，续期后的旧释放标记不能错误封禁；upstream_expires_at 不替代客户订阅期限。节点仅允许有效中转，不能回退原始静态 IP。旧凭据字段为空时兼容 socks5_info 的字段回退。

Hytron 测试库显示 username 有唯一索引，phone 无索引。生产现场索引未核实，不擅自修改业务索引。用户名先走唯一索引；手机号回退由定时按主键分块读取的内存目录定位 id，再按主键取最新账号并重新核对 phone。目录不保存密码，定时替换不可变快照，容量有上限；新手机号最多等待刷新周期。启动目录预热失败时不宣称服务就绪。后续若 DBA 增加 phone 索引，可按明确的配置启用直接索引查找，而非每请求自动判断/扫描。

## 并发与存储

- Go net/http + database/sql + MySQL driver；两个独立共享连接池：source 默认最大 16/空闲 8，会话最大 16/空闲 8，单实例合计最多 32。连接寿命 5 分钟，闲置 1 分钟。上线多实例必须按总实例数预留 MySQL 连接预算。
- source 每个新连接强制 transaction_read_only=1，代码仅查询业务表；会话池只访问独立 bg_client_* InnoDB 表，生产可配置为另一数据库/账号。
- 每次 SQL 有独立 2 秒截止时间，事务最多 3 秒，HTTP 请求 15 秒；连接建立/读写超时明确配置。及时关闭 rows，不在密码校验或其他数据库查询期间持有事务/行锁。
- 全局请求最多 128 在途；密码校验并发 8（可配置），有截止时间的等待；超容量返回可重试 503 和 Retry-After，不清理登录态，不无限排队。IP、账号分别限流，限制器容量有界。
- 默认手机号目录 30 秒更新，最多 250000 个手机号；查询 timeout、并发、容量可配置且启动校验上下限。
- 节点单次 SQL 最多 per_page+1（上限101）行，按 subscription.id 游标分页，不做全量取数、COUNT(*) 或大 OFFSET，不产生每节点额外查询；异常节点可以导致短页，以扫描游标继续。

## 长期会话

延续 30 分钟 access、180 天滑动 refresh、无固定总寿命。随机不透明令牌仅存哈希，重试收据 AES-GCM 加密。设备锁行串行化同账号同设备并发登录；刷新按 session 行→refresh 行固定加锁顺序，跨进程/多实例有效。读取客户和验证密码在写事务外，事务中只做会话操作。

同 request_id 的 120 秒重试返回同一结果；过时代次返回409，不同request_id重放撤销同设备会话。撤销状态必须提交后返回401。事务死锁/锁等待允许有界重试；网络导致 COMMIT 结果未知不盲目重放事务，客户端同 request_id 重试恢复。

## 交付与验证

代码 APP/client-api；PHP 参考版保留在被忽略的 .artifacts/php-previous，Go 通过验证后归档服务器旧 PHP 文件再放 Go 源码/可执行文件。默认仅监听 127.0.0.1:8087，反向代理/SSL/进程托管由用户管理，未启动公开服务。

兼容只读业务库可配置远程 TLS；不能使用跳过证书校验。线上建议独立只读来源账号和仅会话表权限的写账号。测试只连接 Hytron suntest，使用随机 bg_it_* 前缀的独立 fixtures/session 表，完成后只删除本次创建的测试表；不更新客户、订阅或节点业务表。运行 go test -race、真实 MySQL 并发/隔离测试、连接池耗尽/取消/恢复测试、bcrypt/Argon2 PHP 兼容测试及有界压力测试，报告数据量、并发数、成功/拒绝/错误和连接上限，不宣称无限吞吐。

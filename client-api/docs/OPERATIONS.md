# Hytron 测试 API 运行记录

2026-10-11，Asia/Hong_Kong。用户完成宝塔反代后明确要求启动 API，本记录更新最初“仅放置代码、未启动服务”的交付状态。

本文保留 Hytron 测试环境的历史运行记录。新环境安装、本机数据库单账号 v2_test 的只读/表级写权限配置、升级和回滚步骤见 [部署手册](DEPLOYMENT.md)；新的部署配置示例不表示已改动本测试环境的运行账号。

## 当前运行配置

| 项目 | 值 |
| --- | --- |
| API 基址 | `https://test-api.dos.im/api/client/v1` |
| 健康检查 | `https://test-api.dos.im/api/client/v1/health` |
| 主机 | Hytron，`82.152.166.237` |
| 服务 | `bluegrass-client-api.service`，systemd 托管、开机自启、异常退出后自动重启 |
| 运行用户 | 专用系统用户 `bluegrass-api`，非 root、不可交互登录 |
| 程序 | `/www/wwwroot/test-api.dos.im/bin/client-api`，版本 `0.1.0-go` |
| 监听 | `127.0.0.1:8087`，仅通过 Nginx 反代对外提供 HTTPS |
| 私有配置 | `/var/lib/bluegrass-client-api/config.json`，文件 0600、目录 0700，不放入网站目录或 Git |
| 数据库 | source 和 sessions 均为本机 `127.0.0.1:3306/suntest`，账号 `suntest` |
| 业务源 | 只读连接，READ COMMITTED；最大 16 条连接 |
| 会话存储 | 测试库的 `bg_client_sessions`、`bg_client_refresh_tokens`、`bg_client_device_sessions`；最大 16 条连接 |

首次启动已创建上述三个独立会话表，未修改原客户、订阅、中转和 IP 业务表。令牌密钥在服务器生成并保存在私有配置中，重启与更新时必须保留。未连接生产服务器或生产数据库。

systemd 单元安装于 `/etc/systemd/system/bluegrass-client-api.service`，可审阅副本为 [deploy/bluegrass-client-api.service](../deploy/bluegrass-client-api.service)。运行时启用非特权用户、只读系统文件视图及禁止提权等限制；正常启动不执行迁移。

## 管理命令

在 Hytron 管理终端执行：

```sh
systemctl status bluegrass-client-api.service --no-pager
systemctl restart bluegrass-client-api.service
systemctl stop bluegrass-client-api.service
systemctl start bluegrass-client-api.service
journalctl -u bluegrass-client-api.service -n 100 --no-pager
```

只读依赖检查：

```sh
runuser -u bluegrass-api -- /www/wwwroot/test-api.dos.im/bin/client-api \
  -config /var/lib/bluegrass-client-api/config.json -check
```

已有 systemd 进程托管，不要再在宝塔创建占用 8087 的重复进程。每日会话清理仍按 [README](../README.md) 的 `-prune` 命令由管理员安排，本次未额外创建定时任务。

## 本次反代修正

用户的反代已指向 `http://127.0.0.1:8087` 并传递 `X-Real-IP`。验证发现缺少 HTTPS 来源标识，使 HTTPS 请求到达 API 后返回 403 `https_required`。在该站点反代片段增加：

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
```

仅修改 `/www/server/panel/vhost/nginx/proxy/test-api.dos.im/8a5da52ed126447d359e70c05721a8aa_test-api.dos.im.conf`，原文件备份于 `/var/lib/bluegrass-go/private/startup-backup/proxy-20261011T073821Z.conf`。Nginx 配置检查通过后执行平滑重载；站点主配置保持原样，未修改 SSL 证书。后续在宝塔重新生成反代时应保留该请求头及 `X-Real-IP` 的覆盖设置。

## 启动验证

2026-10-11 15:39 左右（HKT）完成以下实时验证：

- `systemctl`：active / running、enabled，启动后重启计数为 0；监听仅为 `127.0.0.1:8087`。
- 服务器按域名访问 HTTPS、独立本机经公网访问 HTTPS，`GET /health` 均返回 200 和 `data.status=ok`，使用正常证书校验。
- 未登录的 `/me`、`/nodes` 返回 401 `unauthorized`；空登录对象返回 422 `validation_failed`。
- 随机不存在的测试用户名登录返回 401 `invalid_credentials`，验证数据库读取及密码登录错误路径；未创建业务测试账号。
- `-check` 确认业务源只读、READ COMMITTED、用户名索引可用和会话表就绪。初始日志未见连接池等待。
- 不带可信 HTTPS 转发标识的本机 HTTP 请求仍返回 403 `https_required`，未关闭 HTTPS 要求。

本轮是启动和接口冒烟验证，没有使用真实客户密码执行成功登录、读取真实节点凭据或进行客户端连接测试；这些不能由健康检查代替。历史隔离集成测试结果见 [verification.md](verification.md)。

## 2026-10-12：哈希字段显示检查（只读）

用户反映三张 API 表的 hash 字段显示乱码。本次通过已有 Hytron 会话核对运行配置，确认 source / sessions 仍指向本机 `127.0.0.1:3306/suntest`，随后使用只读 SQL 连接读取字段元数据和长度统计。连接报告 `CURRENT_USER()=suntest@localhost`、`transaction_read_only=1`；未使用新生产配置，也未连接 `api_all_sunip_cc`。

| 表 | 本次快照行数 | 类型 / 长度检查 |
| --- | --- | --- |
| `bg_client_sessions` | 16 | `device_hash`、`access_hash`、`credential_fingerprint` 均为 BINARY(32)，所有记录长度均为 32 字节 |
| `bg_client_refresh_tokens` | 16 | `token_hash` 为 BINARY(32)，所有记录长度均为 32 字节；`request_hash` 和 `retry_cipher` 当前全部 NULL |
| `bg_client_device_sessions` | 16 | `device_hash` 为 BINARY(32)，所有记录长度均为 32 字节 |

上述二进制列的字符集和排序规则均为空；`id` / `session_id` 为 ASCII 文本，`device_name` 为 utf8mb4 文本。已核对 Hytron 部署目录与本地的 `secure.go`、`session/store.go`、`auth/service.go`，规范化换行后的 SHA-256 相同：普通 hash 使用 SHA-256 原始字节，密码指纹使用 HMAC-SHA256，重试密文使用 AES-256-GCM。

结论：所述 hash 列按文本显示时出现乱字符，符合其二进制存储设计；本次类型和长度检查未见偏离设计。需要可读显示时使用 `HEX()` 查询，见[部署手册](DEPLOYMENT.md)的“哈希字段显示为乱码”。这不是对全部业务数据或登录功能的重新验收。

本次只输出字段定义、行数和字节长度统计，未输出原始哈希、密码、令牌或密文，未解密数据；未修改字段、字符集、表数据、运行配置、服务或账号权限。

## 2026-10-12：成功登录 IP（本地实现，未部署）

本地 API 在 `bg_client_sessions` 增加可空 ASCII `VARCHAR(45)` 字段 `login_ip`，由服务端连接／可信代理识别 IPv4 或 IPv6，与成功登录的会话 INSERT 同事务写入。续期、节点查询和退出不改写它；失败登录不产生记录。仍是三张表，官网业务表和运行账号权限不变。

- 新建表：[01-create-client-tables.sql](../deploy/01-create-client-tables.sql) 已包含新列。
- 已有旧表：[03-add-session-login-ip.sql](../deploy/03-add-session-login-ip.sql) 由 DBA 先补列，再切换新程序；具体步骤见[部署手册](DEPLOYMENT.md)第 5.3 节。
- 本次只修改 APP 本地文件，没有连接任何数据库或服务器，没有执行 SQL、更新 Hytron 服务、提交或推送代码。因此此前核实的 Hytron 表结构尚不含 `login_ip`，直接导出仍是旧结构。

验证：新增的 HTTP→认证→SQL 参数测试先以缺少 `login_ip` 失败，完成实现后通过；13 个 IP 场景涵盖 IPv4/IPv6、映射地址、可信与非可信代理、无效／重复／链式代理头、未知来源，以及 JSON 伪造、NULL 读取、失败登录和 SQL 写失败回滚调用。该测试使用不打开网络连接的数据库驱动，仅验证应用的 SQL 参数和事务调用，不代替 MySQL 引擎的事务验收。

本地 `go test -race -json -count=1 -timeout 3m ./...` 共 362 个测试及子测试通过，52 个需要显式 MySQL 测试配置的用例跳过；`go vet ./...` 通过，Linux amd64 程序交叉编译通过。新增 `TestMySQLLoginIPSurvivesRefreshAndRelogin` 属于跳过的真实 MySQL 用例，本次没有验证实际 ALTER 或真实数据库持久化。已静态核对三份建表定义与补列定义一致、文档示例语法及链接；独立只读代码审查未发现本次范围内的可操作问题。

## 2026-10-12：Hytron 测试库补列（已执行，API 程序未更新）

用户随后明确要求修改 Hytron 数据库。执行前核对已有 Hytron 会话的主机为 `yc-hytron`，API 的 source / sessions 仍均指向 `127.0.0.1:3306/suntest`，实际数据库账号为 `suntest@localhost`，MySQL 8.0.45。三张表为 InnoDB / Dynamic，`login_ip` 尚不存在，账号已有该测试库的 ALTER 权限。

将交付升级脚本限定到测试目标后，实际执行的唯一结构变更为：

```sql
SET SESSION lock_wait_timeout = 5;
ALTER TABLE suntest.bg_client_sessions
    ADD COLUMN login_ip VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NULL DEFAULT NULL,
    ALGORITHM=INSTANT;
```

执行成功后用新连接复查：字段类型 `varchar(45)`、字符集 `ascii`、排序规则 `ascii_bin`、允许 NULL、默认 NULL，与交付定义一致。三张表变更前后均各 17 行；对旧字段逐行计算的摘要仅保留在进程内进行比较，三张表旧数据均一致，未输出或落盘原始字段及摘要。比较 `SHOW CREATE TABLE` 后确认除新增列外，字段、索引和外键均未变化。17 条已有会话的 `login_ip` 全为 NULL，未回填历史 IP。

`bluegrass-client-api.service` 保持 active/running，回环健康接口返回 HTTP 200、`data.status=ok`。本次没有更新或重启 API 程序，没有执行登录请求；实际 IP 写入需后续部署已完成的新版程序。现在从 Hytron 导出的三张表结构包含新列，部署导出仍应只取结构、不携带测试会话数据。没有连接生产数据库、修改官网业务表或账号权限，也没有提交或推送代码。

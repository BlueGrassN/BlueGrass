# Go 客户端 API 部署手册

**当前部署以[网站根目录直接部署教程](DEPLOYMENT-CLIENT-API-SUNIP-CC.md)为准：上传解压、编辑配置、安装固定路径 systemd 服务，更新只替换二进制。本文保留数据库权限、并发与排障资料；其中 `releases/current` 和独立配置目录属于旧方案，不用于这次简化部署，也不要与新的生产 service 模板混用。**

生产域名：`client-api.sunip.cc`；程序根目录：`/www/wwwroot/client-api.sunip.cc`。

更新日期：2026-10-12。适用于 `client-api/` 的 Linux、systemd、宝塔 Nginx 部署。本文提供操作步骤，编写本文没有连接服务器或数据库，也没有执行部署。

**当前确定的生产部署方案：数据库为 `api_all_sunip_cc`，运行账号为 `v2_test`，连接地址为 `127.0.0.1:3306`。其他表只授予 SELECT，三张 API 会话表追加各自所需的读写权限。仍保留 source / sessions 两个连接池；必须新建 3 张 API 表，不需要新建数据库。**

交给部署人员的文件及顺序：

1. [01-create-client-tables.sql](../deploy/01-create-client-tables.sql)：已填写库名 `api_all_sunip_cc`，由 DBA 新建 `bg_client_sessions`、`bg_client_refresh_tokens`、`bg_client_device_sessions`，共 **3 张表**。
2. [02-client-accounts.example.sql](../deploy/02-client-accounts.example.sql)：在私有副本中填写密码，创建或使用本机账号 `v2_test`，授予 `api_all_sunip_cc` 的 SELECT 和三张 API 表的必要读写权限；已有账号按文件说明跳过创建步骤。
3. [config.example.json](../config.example.json)：已填写本机地址、账号和生产库名，复制到私有配置目录，填写相同账号密码和持久密钥，再按本文启动服务。
4. [03-add-session-login-ip.sql](../deploy/03-add-session-login-ip.sql)：仅用于已有三张表、但会话表尚无 `login_ip` 的升级。新部署的第 1 份 SQL 已含该列，不重复执行；仍然共 3 张表。

2026-10-12 本地代码增加成功登录 IP 记录，同日经用户授权已为 Hytron 的 `suntest.bg_client_sessions` 补上 `login_ip`，现在导出其表结构会包含该列。Hytron API 程序尚未更新，现有记录仍为 NULL，不能将补列视作已经启用 IP 写入。新部署使用最新的第 1 份 SQL；其他已有旧结构的部署先按第 5.3 节补列，再使用新程序。本次没有连接生产数据库，详见[运行记录](OPERATIONS.md)。

JSON 配置键仍是 `source`、`sessions`；两者的 `user` 都填 `v2_test`，密码也相同，不要把 JSON 键重命名。建表范围仅为三张 API 表，官网原业务表均不需要新建或修改。真实密码只写入网站目录之外的私有配置，不写进本手册、可提交的配置示例或 SQL。脚本仅供部署人员审阅执行，编写文件没有实际修改数据库。

相关资料：[API 契约](API.md)、[数据库并发设计](DATABASE.md)、[客户端接入文档](../../docs/development/client-api-integration.md)、[Hytron 测试运行记录](OPERATIONS.md)。测试环境的历史验证不代表生产已部署或已通过容量验收。

## 1. 部署结构与写入边界

```text
客户端 --HTTPS--> 宝塔 Nginx --HTTP/回环--> Go API 127.0.0.1:8087
                                             |
                                             +-- source（只读连接池）--------+
                                             |                               |
                                             +-- sessions（会话连接池）------+--> 127.0.0.1:3306
                                                                                  同库、同账号 v2_test
                                                                                  原业务表：只读
                                                                                  API 会话表：按表读写
```

`127.0.0.1` 指 API 所在环境的本机回环地址，本方案要求它能在该地址访问 MySQL。两个池使用同一账号，数据库权限按表区分；source 池额外设置事务只读，sessions 池才执行会话写入。只读连接设置不是独立的只读数据库账号。

| 配置 / 操作 | 数据范围 | 权限和行为 |
| --- | --- | --- |
| `source` | 用户、密码哈希、订阅、节点和当前资源归属 | 普通 `SELECT`，连接启用只读和 `READ-COMMITTED`；不更新业务数据、不建业务索引、不使用业务行锁 |
| `sessions` | `bg_client_sessions`、`bg_client_refresh_tokens`、`bg_client_device_sessions` | 登录、续期、退出会执行 `INSERT` / `UPDATE`；清理使用 `UPDATE` / `DELETE`，短事务内可锁自己的会话行 |
| `-migrate` | `sessions` 指定库中的三个会话表 | 需要建表权限；运行账号 v2_test 不授予该权限。生产由 DBA 预建会话表，正常启动不自动执行迁移 |
| `-check` | 两个连接池、source 只读设置、索引元数据和会话主表 | 只读检查，以同一账号分别建立连接；不验证所有业务查询或会话写权限 |
| `-prune` | API 自己的会话表 | 显式清理一批历史记录；不会清理官网业务表 |

只读查询仍消耗业务数据库的连接、CPU、IO 和元数据锁资源，不能理解为对其他程序零影响。该 API 不实现注册、购买、续费或官网密码修改。

## 2. 环境和版本准备

- 构建环境：Go 1.26 或更高版本，建议使用受维护且经过项目验证的版本；竞态测试还需要受支持的 C 工具链。
- 运行环境：Linux、systemd、MySQL 8/InnoDB、宝塔 Nginx。下文配置辅助命令使用 Python 3；远程 MySQL TLS 需要操作系统可信 CA。
- 数据库：生产使用本机 `127.0.0.1:3306/api_all_sunip_cc` 和账号 `v2_test`，业务表只有读取权限，写权限限于 API 自己的三张表，不将建表权限交给运行账号。Hytron 测试服务当前仍使用 `suntest`；本手册中的生产配置不得直接套到测试服务。
- 网络入口：数据库只允许 API 服务器和其他确有需要的程序来源访问；同机可使用回环，跨主机使用受限网络和验证 TLS 的连接。调整现有监听/防火墙前先确认其他程序依赖，本文不提供一键关闭共享数据库端口的命令。
- 网站：使用独立 API 域名；宝塔站点、证书及续期由站点管理员维护。已有站点不需要重新申请证书。

基础登录、节点查询需要读取 `customers`、`subscriptions`、`proxy_ips`、`forward_rules`、`ny_device_groups`、`forward_plans`。当前本地资源扩展还读取 `spark_instances`、`ipipv_instances`。运行账号必须能看到相关表的索引元数据，启动会检查 `information_schema.statistics` 中的 `customers.username` 前导索引；通常已有表查询权限即可看到相关元数据，不需要额外授予全局管理权限。

以实际发布源码为准确认表与字段权限。`-check` 不检查上述所有表，也不自动修复缺表、缺字段或索引。当前程序使用原业务表名，运行配置没有业务表前缀选项。

业务授权读取应连接主库或具有所需一致性保证的数据源；异步副本延迟会推迟改密、停用和归属变化的生效观察。当前业务时间按 UTC+8 解释，会话时间使用 UTC；保持服务器时钟同步。

### 2.1 构建发布包

在可信构建环境中的 `APP/client-api` 目录执行，下文 shell 示例使用 Bash。先确认所选源码就是本次要发布的版本；工作区尚未提交的 Windows/资源接口改动不能自动视为已验收版本。

```sh
set -euo pipefail
go version
# 不提供数据库测试配置：依赖 MySQL 的集成测试会跳过。
env -u GO_API_TEST_CONFIG go test -race ./...
go vet ./...

release_id='20261012-164709'  # 每次发布使用新的编号，按实际日期修改
mkdir -p "dist/$release_id"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-X main.version=$release_id" \
  -o "dist/$release_id/client-api" ./cmd/client-api
sha256sum "dist/$release_id/client-api"
```

目标服务器 `uname -m` 为 `x86_64` 时使用 `amd64`；为 `aarch64` 时改为 `arm64`。记录构建源码版本、是否有未提交改动、Go 版本、发布编号和 SHA-256。上述测试不等于真实数据库集成测试通过；需要集成测试时仅使用已授权的隔离测试库，绝不将 `GO_API_TEST_CONFIG` 指向生产库。

### 2.2 新部署目录

生产程序部署到 `/www/wwwroot/client-api.sunip.cc`，由 systemd 启动。宝塔仅反代到本机 Go 服务，不需要改变网站运行目录或创建 `public/`；API 路径走反代，其余普通路径返回 404，保留证书续期的专用规则。配置、发布包、源码和备份不放到网站目录中。仅包含 Go 安装配置与 Nginx 修改的教程见 [client-api.sunip.cc 部署教程](DEPLOYMENT-CLIENT-API-SUNIP-CC.md)。

| 路径 | 用途 |
| --- | --- |
| `/www/wwwroot/client-api.sunip.cc/releases/<发布编号>/bin/client-api` | 不可变的发布程序，root 所有 |
| `/www/wwwroot/client-api.sunip.cc/current` | 指向当前发布目录的符号链接 |
| `/var/lib/bluegrass-client-api/config.json` | 运行配置，目录 0700、文件 0600 |
| `/etc/systemd/system/bluegrass-client-api.service` | 唯一服务管理入口 |

由目标 API 服务器管理员进入 root shell 后执行。下面是**首次安装**，已有目录或服务时按第 9 节升级，不覆盖现有配置。

```sh
set -euo pipefail
test "$(id -u)" -eq 0
if ! id bluegrass-api >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/bluegrass-client-api \
    --shell /usr/sbin/nologin bluegrass-api
fi
install -d -o root -g root -m 0755 /www/wwwroot/client-api.sunip.cc
install -d -o root -g root -m 0755 /www/wwwroot/client-api.sunip.cc/releases
install -d -o bluegrass-api -g bluegrass-api -m 0700 /var/lib/bluegrass-client-api

release_id='20261012-164709'  # 必须与本次上传包一致
release_dir="/www/wwwroot/client-api.sunip.cc/releases/$release_id"
test ! -e "$release_dir"
install -d -o root -g root -m 0755 "$release_dir/bin"
# 将下面的上传路径替换为已核对 SHA-256 的程序文件。
install -o root -g root -m 0755 /path/to/upload/client-api "$release_dir/bin/client-api"
sha256sum "$release_dir/bin/client-api"
"$release_dir/bin/client-api" -version
test ! -e /www/wwwroot/client-api.sunip.cc/current
test ! -L /www/wwwroot/client-api.sunip.cc/current
ln -s "$release_dir" /www/wwwroot/client-api.sunip.cc/current
```

Hytron 现有测试服务的程序位于 `/www/wwwroot/test-api.dos.im/bin/client-api`，仓库内 [现有 systemd 单元](../deploy/bluegrass-client-api.service) 也引用这个测试路径。上述 `client-api.sunip.cc` 目录结构用于新部署，不表示已经迁移 Hytron；已有服务必须统一修改程序路径、工作目录和管理命令，不能额外启动第二个占用 8087 的进程。

## 3. 同库单账号与表级权限

由数据库管理员为单一账号 `v2_test` 授予现有数据库的 SELECT，再对三张 API 表追加写权限。`source.user`、`sessions.user` 都使用这个账号；账号不获得官网业务表的写权限，也不授予 `CREATE`、`ALTER`、`DROP`、`GRANT OPTION` 或库级全部权限。Go 业务查询仍使用事务只读连接，会话查询与写入使用独立连接池。

需要新建的表和 `v2_test` 对它们的权限如下，共 **3 张**；其他表只有 SELECT：

| 表 | 权限 | 用途 |
| --- | --- | --- |
| `bg_client_sessions` | `SELECT, INSERT, UPDATE, DELETE` | 登录会话、访问令牌哈希、过期与撤销状态 |
| `bg_client_refresh_tokens` | `SELECT, INSERT, UPDATE, DELETE` | 刷新轮换、重用检测、重试恢复与清理 |
| `bg_client_device_sessions` | `SELECT, INSERT, UPDATE` | 同设备登录串行化与会话关联；当前代码不直接删除此表记录 |

会话表之间有外键，未建立到官网业务表的外键。仅给 `bg_client_sessions` 一张表权限会导致创建/刷新会话失败，不能只因 `-check` 成功就认定权限齐全。

由 DBA 按第 5 节预建这三张表，再按[账号与权限 SQL](../deploy/02-client-accounts.example.sql)授权。SQL 示例使用 `'v2_test'@'127.0.0.1'`，限定本机 TCP 来源。若既有账号实际为 `'v2_test'@'localhost'`，先核对其匹配规则，再将 SQL 中所有 Host 统一改为 `localhost` 并跳过创建账号，不另建第二个同名账号，也不放宽到 `%`。用 `SELECT CURRENT_USER(); SHOW GRANTS FOR CURRENT_USER;` 核对实际匹配的账号和权限；DBA 同时检查是否继承了其他角色或库级写权限。不要通过尝试写业务表来验证权限。

本文只记录配置方案，不连接生产数据库、不实际建表或修改授权；同库部署的确认也不等于授权执行这些生产操作。

## 4. 私有配置

在 `/var/lib/bluegrass-client-api/config.json` 创建如下 JSON，也可复制 `client-api/` 目录中的 [config.example.json](../config.example.json)。先设置 `umask 077`，使用私有编辑器写入；已有文件不覆盖。两段连接都使用 `127.0.0.1:3306`、数据库 `api_all_sunip_cc`、账号 `v2_test` 和同一个密码。密码与令牌密钥只填入私有配置；本地回环数据库示例使用 `tls: "false"`，客户端 API 的 HTTPS 要求仍然开启。

```json
{
  "listen": "127.0.0.1:8087",
  "require_https": true,
  "trusted_proxy_cidrs": ["127.0.0.1/32", "::1/128"],
  "token_key": "REPLACE_WITH_BASE64_OF_32_RANDOM_BYTES",
  "source": {
    "host": "127.0.0.1", "port": 3306,
    "database": "api_all_sunip_cc", "user": "v2_test",
    "password": "REPLACE_DATABASE_PASSWORD", "tls": "false",
    "max_open": 16, "max_idle": 8
  },
  "sessions": {
    "host": "127.0.0.1", "port": 3306,
    "database": "api_all_sunip_cc", "user": "v2_test",
    "password": "REPLACE_DATABASE_PASSWORD", "tls": "false",
    "max_open": 16, "max_idle": 8
  },
  "session_prefix": "bg_client_",
  "max_inflight": 128,
  "hash_workers": 8,
  "hash_wait_seconds": 12,
  "request_timeout_seconds": 15,
  "query_timeout_seconds": 2,
  "transaction_timeout_seconds": 3,
  "phone_refresh_seconds": 30,
  "max_phone_entries": 250000,
  "login_per_minute": 120,
  "refresh_per_minute": 240,
  "read_per_minute": 120,
  "access_ttl_seconds": 1800,
  "refresh_idle_days": 180,
  "retry_seconds": 120
}
```

首次生产配置可执行下面的辅助命令，在私有文件内生成密钥，并只输入一次 `v2_test` 的数据库密码，写入两段连接；不会把密码放进命令参数，也不会打印密钥。脚本拒绝覆盖已经设置过密钥的配置，并检查同库、同账号、指定的本机地址及已确认生产库名。该初始化脚本不用于修改 Hytron 现有测试配置。

```sh
set -euo pipefail
umask 077
python3 - <<'PY'
import base64, getpass, json, os
from pathlib import Path
p = Path('/var/lib/bluegrass-client-api/config.json')
c = json.loads(p.read_text())
if c['token_key'] != 'REPLACE_WITH_BASE64_OF_32_RANDOM_BYTES':
    raise SystemExit('Existing token_key found; preserve it during upgrades.')
if any(c['source'][k] != c['sessions'][k] for k in ('host', 'port', 'database', 'user')):
    raise SystemExit('Source and sessions must use the same database and account.')
if (c['source']['host'], c['source']['port'], c['source']['user']) != ('127.0.0.1', 3306, 'v2_test'):
    raise SystemExit('Expected v2_test at 127.0.0.1:3306.')
if c['source']['database'] != 'api_all_sunip_cc':
    raise SystemExit('Expected production database api_all_sunip_cc.')
c['token_key'] = base64.b64encode(os.urandom(32)).decode('ascii')
password = getpass.getpass('v2_test password: ')
c['source']['password'] = password
c['sessions']['password'] = password
p.write_text(json.dumps(c, indent=2) + '\n')
p.chmod(0o600)
PY
chown bluegrass-api:bluegrass-api /var/lib/bluegrass-client-api/config.json
chmod 0700 /var/lib/bluegrass-client-api
chmod 0600 /var/lib/bluegrass-client-api/config.json
```

`token_key` 必须与 API 会话表一起持久保存并安全备份。升级、重启、回滚时都保留原值；换密钥会使既有会话无法继续使用。测试环境仍使用测试数据库和独立密钥，不能与生产环境共用会话表或密钥。长期续期依赖数据库中的会话记录，不能只备份二进制程序。

### 4.1 TLS 和代理信任

- `tls` 是字符串 `"true"` / `"false"`，不是 JSON 布尔值。
- 同库位于远程服务器时，`source` 和 `sessions` 都必须设为 `"true"`，包括私网地址。当前代码拒绝远程明文连接，不支持 `skip-verify` 或 `preferred`。
- 数据库证书必须由运行环境信任，证书 SAN 必须匹配配置中的数据库主机名或 IP。私有 CA 需按目标 Linux 发行版安装到系统信任库；当前 JSON 没有自定义 CA 文件或客户端证书字段。
- 仅本机回环数据库可使用 `"false"`。网站 HTTPS 证书不能替代数据库 TLS 证书。
- 同机 Nginx 保持回环代理信任。若前面另有 CDN / 负载均衡，应仅在 Nginx 信任准确的上游地址并正确恢复客户端 IP，不能直接信任任意请求带来的 `X-Real-IP` / `X-Forwarded-For`。

## 5. 初始化会话表与依赖检查

### 5.1 由 DBA 预建 API 表并逐表授权

API 只配置单一运行账号 `v2_test`；DBA 使用其既有管理流程预建表，不向 API 提供管理凭据，不新增长期持有 DDL 权限的 API 账号。完整可交付文件为 [01-create-client-tables.sql](../deploy/01-create-client-tables.sql)，以下列出相同的三张表结构，与当前 [会话实现](../internal/session/store.go) 的默认 `bg_client_` 前缀一致。

部署人员先核对目标服务器、已确认库名 `api_all_sunip_cc` 及表名未冲突。下面 `USE` 已与生产配置一致，不需要 `CREATE DATABASE`；使用不同 `session_prefix` 时三个表名和全部外键引用必须同步替换。已有表须比对结构，`IF NOT EXISTS` 不会修复结构差异。

```sql
USE `api_all_sunip_cc`;

CREATE TABLE IF NOT EXISTS `bg_client_sessions` (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    customer_id BIGINT NOT NULL, device_hash BINARY(32) NOT NULL, device_name VARCHAR(120) NOT NULL,
    credential_fingerprint BINARY(32) NOT NULL, access_hash BINARY(32) NOT NULL,
    access_expires_at DATETIME(6) NOT NULL, idle_expires_at DATETIME(6) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0, revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
    login_ip VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NULL DEFAULT NULL,
    UNIQUE KEY(access_hash), KEY(customer_id,device_hash), KEY(idle_expires_at), KEY(revoked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `bg_client_refresh_tokens` (
    token_hash BINARY(32) PRIMARY KEY,
    session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    generation BIGINT NOT NULL, used_at DATETIME(6) NULL, request_hash BINARY(32) NULL,
    retry_cipher BLOB NULL, retry_expires_at DATETIME(6) NULL, created_at DATETIME(6) NOT NULL,
    KEY(session_id), KEY(retry_expires_at), KEY(used_at),
    FOREIGN KEY(session_id) REFERENCES `bg_client_sessions`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `bg_client_device_sessions` (
    customer_id BIGINT NOT NULL, device_hash BINARY(32) NOT NULL,
    session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL, updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY(customer_id,device_hash), KEY(session_id),
    FOREIGN KEY(session_id) REFERENCES `bg_client_sessions`(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

表准备好后，DBA 按 [02-client-accounts.example.sql](../deploy/02-client-accounts.example.sql) 为唯一账号 `v2_test` 授权。已有账号先核对其实际 Host 和权限，再跳过 `CREATE USER`；`GRANT` 不会自动撤回旧权限。以下已填写 `api_all_sunip_cc` 的 SELECT 和三个表的追加权限，账号 Host 按第 3 节匹配现有本机账号：

```sql
GRANT SELECT ON `api_all_sunip_cc`.*
    TO 'v2_test'@'127.0.0.1';
GRANT SELECT, INSERT, UPDATE, DELETE ON `api_all_sunip_cc`.`bg_client_sessions`
    TO 'v2_test'@'127.0.0.1';
GRANT SELECT, INSERT, UPDATE, DELETE ON `api_all_sunip_cc`.`bg_client_refresh_tokens`
    TO 'v2_test'@'127.0.0.1';
GRANT SELECT, INSERT, UPDATE ON `api_all_sunip_cc`.`bg_client_device_sessions`
    TO 'v2_test'@'127.0.0.1';
```

库级权限仅允许 SELECT；INSERT、UPDATE、DELETE 按上面列出的 API 表分别授予，不能提升为库级写权限。`-migrate` 命令仍是程序的显式建表功能，但生产运行配置不应执行它；`v2_test` 无权建表是预期结果，不为通过迁移而临时扩大其权限。今后结构变更仍由 DBA 审阅实施。

### 5.2 用运行身份检查

此步骤会通过两个连接池使用同一账号 `v2_test` 连接本机数据库；应在目标环境、表范围和账号配置经过核对后执行。

```sh
runuser -u bluegrass-api -- /www/wwwroot/client-api.sunip.cc/current/bin/client-api \
  -config /var/lib/bluegrass-client-api/config.json -check
```

输出应包括 `source_read_only: true`、`source_isolation: "READ-COMMITTED"`、`username_indexed: true` 及两池连接预算。`phone_indexed: false` 不阻止当前实现运行，因为手机号登录使用内存目录。

`-check` 不执行写入，因此不能证明会话账号具备写权限，也不检查全部表或完整节点查询。运行时还会预热手机号目录，预热失败不会开始监听；第 8 节需要分别验证服务、数据库依赖和实际登录流程。

### 5.3 已有表增加登录 IP

新程序需要 `bg_client_sessions.login_ip`，必须先升级表结构，再替换程序；`-check` 成功不能代替字段核对。DBA 在确认的目标库中先查询：

```sql
SHOW COLUMNS FROM bg_client_sessions LIKE 'login_ip';
```

没有该列时，审阅并执行 [03-add-session-login-ip.sql](../deploy/03-add-session-login-ip.sql)。该文件只对会话表添加 `VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NULL DEFAULT NULL` 列，不建第 4 张表、不重建或清空会话、不改官网业务表，也不调整账号权限。默认 `USE api_all_sunip_cc`；若在 Hytron 测试，必须在私有副本中明确改为 `suntest`，不可混用目标。

脚本使用 MySQL 8 的 `ALGORITHM=INSTANT` 并将元数据锁等待限定为 5 秒；不支持瞬时加列或等待超时时会报错，应由 DBA 评估，不自动退回整表复制。新建表已含此列时不执行升级脚本。旧程序会忽略新增的可空列，所以可先补列再升级；回滚程序时保留该列。

`login_ip` 保存成功登录时服务端识别的来源 IP，支持 IPv4/IPv6，不含端口；`customer_id` 对应客户，`created_at` 是该次登录的 UTC 时间。它在原有会话 INSERT 中一并写入，不增加查询、事务或锁。已有会话及无法识别来源的内部调用为 NULL，不猜测或回填历史数据。续期、获取节点和退出不会改写该 IP；重新登录建立新会话并记录新的 IP，不按 IP 绑定或限制会话。

只采信 `trusted_proxy_cidrs` 内代理提供的单个有效 `X-Real-IP`，其余情况使用 TCP 对端 IP；无效或缺失代理头可能记录代理地址。Nginx 必须覆盖 `X-Real-IP $remote_addr`；存在 CDN / 上游代理时先配置准确的可信来源和 real_ip 恢复规则，否则不能把记录解释为最终客户公网 IP。客户端不得在 JSON 中提交 `login_ip` / `client_ip`，接口也不返回这两个内部字段。

管理员可用只读查询查看某客户的成功登录记录（将示例客户 ID 替换为实际 ID）：

```sql
SELECT customer_id, id AS session_id, device_name, login_ip,
       created_at AS login_at_utc, revoked_at
FROM bg_client_sessions
WHERE customer_id = 12345
ORDER BY created_at DESC
LIMIT 50;
```

这不是独立永久审计表，也不记录失败尝试；记录跟随会话保留，执行 `-prune` 时会清除闲置到期或撤销超过 30 天的会话及其 IP。

## 6. systemd 托管

新部署将以下内容保存为 `/etc/systemd/system/bluegrass-client-api.service`。已有 Hytron 单元使用不同程序路径，应审阅后统一调整，不直接套用覆盖。

```ini
[Unit]
Description=BlueGrass Client API
After=network.target mysqld.service mysql.service
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
User=bluegrass-api
Group=bluegrass-api
WorkingDirectory=/www/wwwroot/client-api.sunip.cc/current
ExecStart=/www/wwwroot/client-api.sunip.cc/current/bin/client-api -config /var/lib/bluegrass-client-api/config.json
Restart=on-failure
RestartSec=3
TimeoutStopSec=25
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
CapabilityBoundingSet=
LimitNOFILE=16384
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

```sh
set -euo pipefail
systemd-analyze verify /etc/systemd/system/bluegrass-client-api.service
systemctl daemon-reload
systemctl enable --now bluegrass-client-api.service
systemctl status bluegrass-client-api.service --no-pager
journalctl -u bluegrass-client-api.service -n 100 --no-pager
ss -lntp '( sport = :8087 )'
```

确认服务为 `active (running)` 且仅监听 `127.0.0.1:8087`。不要同时启用宝塔 Go 项目守护、Supervisor 或手动后台进程管理同一端口。服务不以 root 身份运行；程序和发布目录也不应由运行用户写入。

## 7. 宝塔反向代理和入口限制

宝塔站点域名为 `client-api.sunip.cc`，继续管理 HTTPS 和证书续期。保留已有网站目录、运行目录与 Nginx `root`；Go 服务不依赖静态根目录运行。API 反代到 `http://127.0.0.1:8087`，保留 `/api/client/v1/...` 完整路径，关闭代理缓存。仅在这个 API 站点移除不需要的 PHP、静态文件和伪静态规则（包括 include 内的规则），非 API 普通路径返回 404，保留证书续期的专用规则。

当前 Go 读取限流发生在查库鉴权之后，无法单独限制无效令牌造成的查库频率。公开服务需在 Nginx 或可信网关增加**鉴权前**的 IP 与全局限制。下面数值仅为可调整的起点，须在测试环境结合共享出口、多设备、批量刷新及数据库预算验收，不是已验证的生产容量。

在 Nginx 的 `http {}` 上下文中定义一次，不能放进站点的 `server {}` 或 `location {}`：

```nginx
limit_req_zone $binary_remote_addr zone=bg_api_ip:10m rate=10r/s;
limit_req_zone $server_name zone=bg_api_total:1m rate=50r/s;
limit_conn_zone $binary_remote_addr zone=bg_api_conn_ip:10m;
limit_conn_zone $server_name zone=bg_api_conn_total:1m;
log_format bg_api_safe '$remote_addr [$time_local] "$request_method $uri" '
                       '$status $body_bytes_sent $request_time';
```

在这个 API 专用域名的 HTTPS `server {}` 内合并以下片段，保留原 `listen`、`server_name`、证书和续期配置。先处理宝塔已生成的重复 `location`、PHP/静态文件规则与反代片段，不能重复粘贴多个相同路径；确认非 API 路径不会落到源码目录的静态文件服务。

```nginx
access_log /www/wwwlogs/bluegrass-api.access.log bg_api_safe;
client_max_body_size 4k;
client_body_timeout 10s;
limit_req_status 429;
limit_conn_status 429;

location ^~ /api/client/v1/ {
    limit_req zone=bg_api_ip burst=20 nodelay;
    limit_req zone=bg_api_total burst=100 nodelay;
    limit_conn bg_api_conn_ip 16;
    limit_conn bg_api_conn_total 128;
    error_page 429 = @bg_api_rate_limited;
    error_page 413 = @bg_api_payload_large;

    proxy_pass http://127.0.0.1:8087;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Connection "";
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_connect_timeout 2s;
    proxy_send_timeout 20s;
    proxy_read_timeout 20s;
    proxy_cache off;
    proxy_intercept_errors off;
}

location @bg_api_rate_limited {
    default_type application/json;
    add_header Cache-Control "no-store" always;
    add_header Retry-After "2" always;
    return 429 '{"success":false,"error":{"code":"rate_limited","message":"Too many requests"}}';
}

location @bg_api_payload_large {
    default_type application/json;
    add_header Cache-Control "no-store" always;
    return 413 '{"success":false,"error":{"code":"payload_too_large","message":"Payload too large"}}';
}

# API 以外的普通路径返回 404；保留宝塔证书续期的专用规则。
location / {
    return 404;
}
```

网关自身的 429/413 返回不含应用 `server_time`；客户端需要先按 HTTP 状态处理，不能把这种响应当成令牌失效。不要使用 `proxy_pass http://127.0.0.1:8087/;` 的尾部斜杠版本，否则这里会改写 API 前缀。

`X-Real-IP` 和 `X-Forwarded-Proto` 必须由可信代理覆盖，不能原样转发客户端提供的值。此例假设 TLS 在同机宝塔终止；若有其他 TLS 终止层，应单独核实可信链路。将 HTTP 请求重定向到 HTTPS，不通过关闭 `require_https` 解决 403。

宝塔常用 Nginx 路径如下，实际安装路径不同时使用对应二进制。先检查成功，再平滑重载：

```sh
set -euo pipefail
/www/server/nginx/sbin/nginx -t
/www/server/nginx/sbin/nginx -s reload
```

定义 `limit_*_zone` 但没有在请求路径应用 `limit_req` / `limit_conn` 不会限流。这些限制按 Nginx 实例生效，多入口需要统一预算；它们不能替代上游的带宽攻击防护。日志使用不含 query 的 `$uri`，不要新增 Authorization、请求体、密码、完整令牌或节点凭据日志；检查宝塔/WAF/CDN 的独立日志设置同样不记录这些字段。

## 8. 上线验证

### 8.1 不使用客户凭据的检查

```sh
# 仅在 API 主机上，用可信回环请求检查进程响应。
curl --fail-with-body --max-time 5 \
  -H 'X-Forwarded-Proto: https' \
  http://127.0.0.1:8087/api/client/v1/health

# 在外部机器执行。生产域名为 client-api.sunip.cc，Hytron 测试域名仍为 test-api.dos.im。
api_origin='https://client-api.sunip.cc'
curl --fail-with-body --max-time 10 "$api_origin/api/client/v1/health"
curl --max-time 10 -sS -o /dev/null -w '%{http_code}\n' "$api_origin/api/client/v1/me"
curl --max-time 10 -sS -o /dev/null -w '%{http_code}\n' "$api_origin/api/client/v1/nodes"
curl --max-time 10 -sS -o /dev/null -w '%{http_code}\n' "$api_origin/.env"
curl --max-time 10 -sS -o /dev/null -w '%{http_code}\n' "$api_origin/.git/config"
```

预期 `/health` 返回 200 且 `data.status=ok`，未带认证的 `/me`、`/nodes` 返回 401；敏感路径应为 403/404，不能返回文件内容。不使用 `curl -k` 跳过证书验证，也不自动跟随重定向去发送凭据。

**`/health` 是进程存活检查，不查询数据库。** 数据库在启动后断开时它仍可能返回 200。另行使用 `-check` 验证依赖；该命令会产生额外连接，不宜作为高频探针。

### 8.2 受控账号的功能验证

用当前环境允许使用的测试账号，通过客户端或安全的接口调试工具完成以下顺序。真实密码和令牌不要出现在 shell 命令行、历史、截图或提交文件中。

1. 调用 `/auth/login` 成功，确认仅写入同库中的 API 会话表，并核对该会话的 `login_ip` 与预期来源一致。登录会写入这些表，不是纯只读测试；续期后该字段应保持原登录 IP。
2. 使用访问令牌调用 `/me`、节点列表和本人节点详情，确认归属和有效期。节点响应包含连接凭据，不输出到共享日志。
3. 调用 `/auth/refresh`，使用同一 `request_id` 验证重试恢复，随后用新令牌查询。正常重启后会话应仍可使用。
4. 调用 `/auth/logout` 后，该设备会话应失效；官网会话和业务数据不应被修改。
5. 若发布包含资源扩展，登录后检查 `/capabilities` 和对应资源接口。旧测试服务器可能没有这些端点；404 不能解释为用户没有资源，也不能用本地新源码证明旧服务器已更新。
6. 在隔离测试环境检查请求体超限、无效令牌频率限制、并发登录和连接池等待。不要在生产库做压测或尝试写业务表验证权限；生产功能验证仅限已获授权的账号及 API 会话表写入。

记录发布编号、程序哈希、实际账号权限、监听地址、各检查结果及未验证项。仅有健康检查成功，不能宣称登录、会话写入、全部节点查询或生产容量已验收。

## 9. 升级、回滚与备份

升级前安全备份运行配置（含原 `token_key`）、三个 API 会话表、当前 unit/站点配置，记录 `readlink -f /www/wwwroot/client-api.sunip.cc/current`。由 DBA 使用一致性备份流程保存这三个关联表；不为 API 备份而扩大运行账号权限。备份不放网站目录或 Git，API 会话备份属于敏感数据；官网整库备份继续由原数据库维护流程管理。

将新程序安装到新的 `releases/<发布编号>/bin/client-api`，核对 SHA-256 和 `-version`。使用**新二进制**及原运行配置执行 `-check`。先在测试环境验证与现有会话结构兼容；增加登录 IP 的版本必须先完成第 5.3 节字段核对和必要的补列。`CREATE TABLE IF NOT EXISTS` 不会为旧表加列；不在升级中例行执行 `-migrate`，不覆盖密钥、不清空会话表。

确认可切换后，在 API 服务器 root shell 执行：

```sh
set -euo pipefail
new_release='/www/wwwroot/client-api.sunip.cc/releases/REPLACE_NEW_RELEASE_ID'
test -x "$new_release/bin/client-api"
test ! -e /www/wwwroot/client-api.sunip.cc/current.next
test ! -L /www/wwwroot/client-api.sunip.cc/current.next
ln -s "$new_release" /www/wwwroot/client-api.sunip.cc/current.next
mv -Tf /www/wwwroot/client-api.sunip.cc/current.next /www/wwwroot/client-api.sunip.cc/current
systemctl restart bluegrass-client-api.service
systemctl status bluegrass-client-api.service --no-pager
```

按第 8 节重新验证；单实例重启存在短暂中断，客户端应退避重试而不是清除会话。只替换二进制无需 `daemon-reload`；修改 unit 后才需要。

回滚时将上面 `new_release` 改成事先记录的旧发布目录，执行同样的符号链接切换和重启，再验证。保留现有运行配置、密钥和会话数据，不把旧会话表快照直接恢复到正在运行的服务：旧快照可能恢复已经撤销/消费的令牌状态。若未来版本修改会话结构，必须先确认旧程序兼容，否则不能只回滚二进制。

API 会话灾难恢复时，将这三个会话表及其匹配的密钥作为整体处理，并单独评估令牌撤销状态。因与官网共库，不能为回滚或恢复 API 而覆盖整个生产数据库，也不能将测试数据还原到生产库。恢复范围仅限 DBA 已核定的 API 表；恢复流程需要另行演练，本文没有实际执行恢复。

## 10. 清理、容量和排障

### 10.1 会话清理

可由管理员在宝塔计划任务或 systemd timer 中安排每日一次，使用运行账号执行：

```sh
runuser -u bluegrass-api -- /www/wwwroot/client-api.sunip.cc/current/bin/client-api \
  -config /var/lib/bluegrass-client-api/config.json -prune
```

一次调用执行一个短事务，每条清理语句有 `LIMIT 1000`；父会话删除还会触发外键级联，因此不能把它解释为所有表总共最多删除 1000 行。清理范围是过期重试密文、保留期外已用令牌、超过保留期的过期/撤销会话；未必一次清空积压。观察耗时、共享数据库压力和积压后再调整频率，不使用无限循环集中清理。编写本文未创建任何计划任务。

### 10.2 容量预算与当前限制

| 项目 | 默认值 / 部署含义 |
| --- | --- |
| source / sessions 连接池 | 同库双池各最多 16 条，单 API 进程合计最多 32 条，多进程按数量相乘；配置允许两池合计最高 128，并不表示数据库承受得住 |
| 请求与密码工作 | 最多 128 个在途请求，8 个密码工作位，密码排队最多 12 秒 |
| 超时 | 请求 15 秒，单次业务查询 2 秒，会话事务 3 秒；不要仅靠调大超时掩盖慢查询 |
| 应用限频 | 登录 120/分钟/IP，续期 240/分钟/IP，读取 120/分钟/会话；均为单 Go 实例额度，读取额度在鉴权查库后应用 |
| 手机号目录 | 启动预热，之后每轮结束等待 30 秒再刷新；按主键每批读 2000 行，最多 25 万个非空手机号键；即使没有登录流量也有后台读取负载 |
| 会话寿命 | 访问令牌 30 分钟；刷新令牌空闲 180 天，成功续期滑动延长；密钥、数据库丢失或账号状态变化仍可能要求重新登录 |
| 多实例 | 需共享同库中的同一组会话表和密钥、配置一致；限频、在途请求和手机号目录是各实例独立的，不能直接按单机预算横向复制 |

当前方案必须将两池连接数相加，再乘 API 实例数量，并加上官网、其他程序、管理与故障重连余量，统一计入同一 MySQL 的容量预算。使用同一账号不会把两个池自动合并成一个池；还需核对该账号的连接数限制能覆盖两池预算。表级权限限制写入范围，但不会隔离 CPU、IO、连接或日志资源；会话写入、清理和手机号目录读取都要计入共享负载。业务表只读权限不能阻止高频读取耗尽资源，也不能阻止账号泄露后读取其被授权的数据。

程序每分钟记录 `source_open`、`source_in_use`、`source_waits` 及对应 session 指标。`*_waits` 是进程启动以来的累计值，应观察增长速度；结合 429/503、延迟、CPU/内存和数据库慢查询判断瓶颈。当前没有自动告警平台或完整审计系统，需接入现有监控；systemd 模板也未设置 CPU/内存硬上限，设置前应在测试环境确认密码计算及目录预热需求。

### 10.3 常见问题

| 现象 | 优先检查 |
| --- | --- |
| `configuration file must be private (mode 0600)` | 配置是否 0600、是否由运行用户可读，父目录是否可进入；不要通过放宽到 0644 解决 |
| `remote database connections require verified TLS` | 远程库的 `tls` 是否为字符串 `"true"`；核对 CA、主机名与证书 SAN，不关闭校验 |
| `source database unavailable` / `session database unavailable` | 分别核对目标、端口、网络、账号 Host 匹配、密码和 TLS；通用错误有意不泄露数据库细节 |
| `session schema missing` | 共同库名/会话前缀是否正确、DBA 是否预建三个表、会话账号是否有 SELECT；该错误也可能是权限不足，不用运行账号执行迁移或扩大其权限 |
| `customers.username must have a leading index` | 由业务库管理员确认现有索引与元数据可见性；API 不自动添加索引 |
| `phone directory warmup failed` | 客户表查询权限、字段、网络和查询耗时、手机号键数是否超预算；预热总期限 30 秒，每批仍受查询超时限制 |
| HTTPS 域名返回 `403 https_required` | 可信代理地址和覆盖后的 `X-Forwarded-Proto`；保持 `require_https=true` |
| `-check` 成功但登录/续期失败 | 是否仅授权了一张表、三张表各自所需读写权限是否齐全、密码校验/账号状态、共享数据库压力；健康检查不能覆盖这些路径 |
| 429 / 503 增多 | 区分 Nginx 与应用限流，查看连接池等待、密码并发和数据库负载；客户端退避，不先扩大连接池 |
| `502` 或 8087 端口被占用 | unit 路径、服务日志、是否重复启用宝塔守护或手工进程 |
| `start-limit-hit` | 修复实际配置/依赖错误后执行 `systemctl reset-failed bluegrass-client-api.service` 再启动，避免无限重启 |
| 更新后所有客户端重新登录 | 是否误换了密钥、数据库目标、会话前缀，或丢失了客户端持久化状态 |

### 10.4 哈希字段显示为乱码

三张 API 表中的哈希采用 `BINARY(32)` 存储原始 32 字节，而非十六进制文本；字段没有字符集。数据库管理工具按文字解释这些字节时，可能显示乱字符、不可打印字符或方块，属于显示方式问题，不应据此修改表字符集或字段类型。

| 字段 | 格式与含义 |
| --- | --- |
| `device_hash`、`access_hash`、`token_hash`、`request_hash` | SHA-256 原始摘要，32 字节；`request_hash` 可为 NULL |
| `credential_fingerprint` | 对官网密码哈希计算的 HMAC-SHA256 指纹，32 字节，用于检测改密，不是明文密码 |
| `retry_cipher` | AES-256-GCM 加密的续期重试结果，BLOB 二进制密文，可为 NULL；它不是哈希 |
| `id`、`session_id`、`device_name` | 文本字段；若这些字段显示异常，需另查输入或显示编码，不能用哈希格式解释 |

需要在管理员工具中阅读哈希时，使用只读查询转换显示形式：

```sql
SELECT id,
       HEX(device_hash) AS device_hash_hex,
       HEX(access_hash) AS access_hash_hex,
       HEX(credential_fingerprint) AS credential_fingerprint_hex,
       OCTET_LENGTH(access_hash) AS access_hash_bytes
FROM bg_client_sessions
LIMIT 5;
```

`HEX()` 只改变查询结果的显示，不修改存储；32 字节会显示为 64 个十六进制字符。不要把转换结果 UPDATE 回原字段，也不要将 `BINARY(32)` 改成 `VARCHAR`。哈希不需要也不能通过字符集转换恢复为原始令牌。

2026-10-12 对 Hytron 的 `suntest` 进行了只读类型/长度检查，结果见[运行记录](OPERATIONS.md)；该检查未连接生产库，未读取或输出真实令牌、密码、完整哈希或解密内容。

## 11. 发布验收记录

将下列项目作为本次发布记录，实际执行后再勾选，不以本文存在代替验收：

- [ ] 发布编号、源码版本、程序 SHA-256 已记录，未将本地未部署功能当作在线功能。
- [ ] 生产两池连接 `127.0.0.1:3306/api_all_sunip_cc`，均使用 `v2_test` 和同一密码；三个 API 表及授权与交付 SQL 一致。
- [ ] 会话表已有 `login_ip`；已有表升级由 DBA 先补列，再替换程序。成功登录记录的来源 IP 已核对代理信任配置。
- [ ] v2_test 只有库级 SELECT 和三张 API 表的必要读写权限，无 DDL 或其他业务表写权限；source 池启用事务只读，会话表已由 DBA 预建。
- [ ] 远程 MySQL 验证 TLS，数据库入口按合法来源限制；Go 只监听回环，配置与备份不在公开目录。
- [ ] `-check`、手机号预热、受控账号登录/续期/节点/退出分别验证，生产业务数据未改动。
- [ ] Nginx 语法检查通过，HTTPS/转发路径/代理信任正确，请求体和鉴权前限流已验证。
- [ ] 共享数据库预算、手机号目录后台负载、多实例额度已评估；没有用生产库做压测。
- [ ] 配置、原密钥、三个 API 会话表有安全备份，保留可用旧版本；API 恢复不覆盖整个共享数据库。
- [ ] 敏感字段不进入日志，监控和清理任务有明确负责人；测试与生产状态分别记录。

# BlueGrass Client API — Go

独立的客户端登录、长期续期和节点 API。运行时使用 Go + MySQL 8/InnoDB，不依赖 PHP、Laravel 或 SQLite。原 PHP 参考代码已归档到忽略目录 `.artifacts/php-previous`。

- 本地源码：`E:\workspace\APP\client-api`
- Hytron 网站代码目录：`/www/wwwroot/test-api.dos.im`
- 私有开发验证目录：`/www/wwwroot/bluegrass-client-api-go`
- 接口：[docs/API.md](docs/API.md)
- 当前 `.vip` 域名的 EO 接入：[简明部署说明](docs/EDGEONE-CLIENT-API-SUNIP-VIP.md)、[本站 Nginx 配置](deploy/client-api.sunip.vip.nginx-site.conf)、[反代配置](deploy/client-api.sunip.vip.nginx-proxy.conf)。直接使用 `EO-Connecting-IP`，缺失时使用实际连接 IP；只替换本站两份配置，无需更新 Go 程序。
- 生产库并发设计：[docs/DATABASE.md](docs/DATABASE.md)
- 部署手册：[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)（同库单账号 v2_test、业务表只读与会话表级权限、宝塔反代、初始化、升级与回滚）
- 本次生产部署：[网站根目录直接部署教程](docs/DEPLOYMENT-CLIENT-API-SUNIP-CC.md)：上传解压后直接使用 `client-api` 和 `config.json`，不改宝塔运行目录；[生产 systemd 模板](deploy/bluegrass-client-api.production.service) 使用固定路径和宝塔的 `www` 用户。更新时停止服务、覆盖二进制、启动服务。
- Nginx 仅编辑本域名的 `client-api.sunip.cc.conf`：[站点文件顶部片段](deploy/client-api.sunip.cc.site-prefix.conf.example) 放在 `server {}` 外，[反代片段](deploy/client-api.sunip.cc.server.conf.example) 放在本域名 HTTPS `server {}` 内，不修改主配置或其他站点。
- 数据库交付：[新建 3 张表的 SQL](deploy/01-create-client-tables.sql)、[v2_test 账号及授权 SQL](deploy/02-client-accounts.example.sql)、[配置示例](config.example.json)。
- 当前运行状态与服务管理：[docs/OPERATIONS.md](docs/OPERATIONS.md)

## 构建与测试

Go 1.26+（本次使用经官方 SHA-256 校验的 Go 1.27.2）。

```sh
go test -race ./...
go vet ./...
go build -trimpath -o bin/client-api ./cmd/client-api
```

MySQL 集成测试需 `GO_API_TEST_CONFIG=/private/mysql-test.json`，内容是数据库连接对象。测试代码只允许 `127.0.0.1` 的 `suntest`，每次建立随机 `bg_it_*` 表，退出时只清理本次表；绝不复用或清空业务表。未指定测试连接时，依赖 MySQL 的测试会明确跳过，不能当作集成验证通过。

## 运行

生产两池都使用 `127.0.0.1:3306/api_all_sunip_cc` 和账号 `v2_test`，填写相同密码，并保存持久的32字节随机 token_key。当前简化部署包在网站根目录放置私有 `config.json`，必须使用对应 Nginx 模板屏蔽文件下载，文件权限0600、所有者www；真实配置不提交到仓库。简化包已生成 token_key，后续更新仅替换二进制。源码开发可将 `config.example.json` 复制到网站之外的私有位置使用。Hytron 现有测试服务的私有配置路径及数据库没有随本次文档变化而修改。

成功登录 IP 记录在 `bg_client_sessions.login_ip`，仍只需三张 API 表。[最新建表 SQL](deploy/01-create-client-tables.sql) 已含字段；已有旧表由 DBA 先执行[登录 IP 升级 SQL](deploy/03-add-session-login-ip.sql)，再替换程序。2026-10-12 已为 Hytron 测试库补列，导出结构会包含该字段；API 程序尚未更新，实际登录 IP 写入尚未启用。代理信任、记录范围和查看方法见[部署手册](docs/DEPLOYMENT.md)第 5.3 节。

下方 `-migrate` 仅用于具备建表权限的测试初始化场景。当前同库单账号方案由 DBA 预建 API 表，运行账号跳过这一步；连接配置使用[部署手册](docs/DEPLOYMENT.md)中的本机、同库、同账号示例。

```sh
# 仅首次初始化生成 key，后续部署保留原值
./bin/client-api -generate-key
./bin/client-api -config /private/config.json -migrate
./bin/client-api -config /private/config.json -check
./bin/client-api -config /private/config.json
```

`-migrate` 只创建 `session_prefix` 指定的独立会话表，不迁移业务表。正常启动不自动执行 DDL。单一账号 `v2_test` 拥有库级 SELECT，写权限仅追加到 API 自己的表；source 池额外启用事务只读，sessions 池执行会话读写。DBA 按交付 SQL 预建 **3 张表**：`bg_client_sessions`、`bg_client_refresh_tokens`、`bg_client_device_sessions`，再授权；运行账号无建表权限，不执行上面的 `-migrate` 步骤。完整步骤见[部署手册](docs/DEPLOYMENT.md)。

默认监听 `127.0.0.1:8087`。Hytron 当前由 `bluegrass-client-api.service` 托管，用户的宝塔站点提供反向代理和 HTTPS；不要再建立重复进程。保持 `require_https=true`，由可信反向代理**覆盖设置** `X-Forwarded-Proto` 和 `X-Real-IP`。这是 Go HTTP 服务，不再使用 PHP 的 `public/` 运行目录。

初次交付仅开发和放置代码。2026-10-11 用户另行要求启动后，已创建私有配置、初始化测试库独立会话表、启用 systemd 服务和开机自启，并补齐该站点的 HTTPS 转发请求头。公网健康检查已通过，仍仅使用 `suntest` 测试库；操作范围与验证证据见运行记录。

每日可由管理员运行一次或多次有界清理命令（每条清理语句最多处理1000条，父会话删除还会触发外键级联，不代表所有表总共最多删除1000条）：

```sh
./bin/client-api -config /private/config.json -prune
```

部署时保留私有配置、token_key 和 API 自己的会话表数据；同库方案不能为了 API 回滚而覆盖整个生产数据库。key 变更会使既有会话失效，不要每次部署重新生成。网站原 Sanctum Token 不可用于此 API。

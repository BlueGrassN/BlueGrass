# BlueGrass Client API — Go

独立的客户端登录、长期续期和节点 API。运行时使用 Go + MySQL 8/InnoDB，不依赖 PHP、Laravel 或 SQLite。原 PHP 参考代码已归档到忽略目录 `.artifacts/php-previous`。

- 本地源码：`E:\workspace\APP\client-api`
- Hytron 网站代码目录：`/www/wwwroot/test-api.dos.im`
- 私有开发验证目录：`/www/wwwroot/bluegrass-client-api-go`
- 接口：[docs/API.md](docs/API.md)
- 生产库并发设计：[docs/DATABASE.md](docs/DATABASE.md)
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

将 `config.example.json` 复制到**网站目录以外**的私有位置，例如 `/var/lib/bluegrass-client-api/config.json`，权限0600，并配置数据库密码及32字节随机 token_key。该文件不能放在公开网站根目录。

```sh
# 仅首次初始化生成 key，后续部署保留原值
./bin/client-api -generate-key
./bin/client-api -config /private/config.json -migrate
./bin/client-api -config /private/config.json -check
./bin/client-api -config /private/config.json
```

`-migrate` 只创建 `session_prefix` 指定的独立会话表，不迁移业务表。正常启动不自动执行 DDL。建议生产使用独立的会话数据库及写账号；来源账号只需查询现有业务表及索引元数据。

默认监听 `127.0.0.1:8087`。Hytron 当前由 `bluegrass-client-api.service` 托管，用户的宝塔站点提供反向代理和 HTTPS；不要再建立重复进程。保持 `require_https=true`，由可信反向代理**覆盖设置** `X-Forwarded-Proto` 和 `X-Real-IP`。这是 Go HTTP 服务，不再使用 PHP 的 `public/` 运行目录。

初次交付仅开发和放置代码。2026-10-11 用户另行要求启动后，已创建私有配置、初始化测试库独立会话表、启用 systemd 服务和开机自启，并补齐该站点的 HTTPS 转发请求头。公网健康检查已通过，仍仅使用 `suntest` 测试库；操作范围与验证证据见运行记录。

每日可由管理员运行一次或多次有界清理命令（每批最多1000条）：

```sh
./bin/client-api -config /private/config.json -prune
```

部署时保留私有配置、token_key 和客户端会话数据库。key 变更会使既有会话失效，不要每次部署重新生成。网站原 Sanctum Token 不可用于此 API。

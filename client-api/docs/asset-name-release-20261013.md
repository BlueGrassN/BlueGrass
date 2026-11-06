# 资产名称更新说明（2026-10-13）

**此包已由[同时返回资产名称和客户备注的新版](asset-name-and-remark-release-20261013.md)取代。下文保留资产名称单独修复时的验证记录；当前部署请使用新版更新包。**

## 核查结论

2026-10-12 交付的 `client-api-linux-amd64.zip` / `client-api-linux-arm64.zip` 内二进制，与发布快照 `20261012-164709` 的对应二进制相同。该快照中的节点和资源查询读取 `subscriptions.customer_remark`，名称映射优先使用备注，最后追加 ` #ID`。如果生产仍使用这份二进制，就会出现备注替代资产名称的现象。

当前本地已有资产名称修复及回归测试，但旧部署包未包含它。本次将这些现有修复构建为二进制更新包；没有连接生产服务器、生产数据库或公共 API，未验证或替换线上二进制。

## 更新后的字段

继续使用现有 JSON 字段 `name`，不增加字段，不改变资源 ID：

- 从 `proxy_ips.asset_name` 获取真实资产名称，去除首尾空白，直接返回，不追加 ID。
- 不读取 `customer_remark`，也不使用备注作为空名称的回退值。
- 适用于 `/nodes`、`/nodes/{id}`、`/resources`、`/resources/{kind}/{id}`、`/resources/expiry`，均位于 `/api/client/v1` 下。
- 空资产名称仍使用中性名称：节点为 `国家名称 #ID` 或 `Node #ID`；资源摘要为 `Resource #ID`。待交付/待审核的资源摘要仍不展示未经确认的资产名称。

例如资产名称为 `美国-198.87.96.137`、客户备注为 `Customer label`、订阅 ID 为 `11`：旧版返回 `Customer label #11`，新版返回 `美国-198.87.96.137`，独立的 `id` 仍是 `11`。

## 更新文件

- Intel / AMD：[client-api-asset-name-linux-amd64.zip](../dist/client-api-asset-name-linux-amd64.zip)
- ARM64：[client-api-asset-name-linux-arm64.zip](../dist/client-api-asset-name-linux-arm64.zip)
- [构建与校验记录](../dist/client-api-asset-name-BUILD-INFO.json)

每个 ZIP **只有一个根目录文件 `client-api`**，不包含配置文件、密钥、SQL、Nginx 或 systemd 配置。

更新流程：**停止 `bluegrass-client-api.service` → 覆盖 `/www/wwwroot/client-api.sunip.cc/client-api` 并保持可执行权限 → 启动服务**。保留原来的 `config.json`、`token_key` 和会话数据，不需要修改数据库或反代。

## 本地验证

构建标识：`20261013-101141-asset-name`。在原发布源码与其 vendored 依赖的独立副本中，纳入本地已经存在的两份名称映射修复和两份对应测试；运行代码相对原发布只涉及 `internal/source/nodes.go` 与 `internal/source/resources.go`。查询数量、WHERE 归属条件、分页、连接池、认证和会话写入逻辑未修改。

先将新回归测试放到旧实现上，得到 53 个失败测试及子测试，复现了备注覆盖、额外 ID 后缀和备注回退问题；再使用修复后的实现验证：

- `go test -race -json -count=1 -timeout 5m ./...`：417 个测试及子测试通过，52 个需要 MySQL 的测试跳过，0 失败。
- `go vet ./...`：通过。
- `GO_API_TEST_CONFIG` 清空，依赖通过已交付的 vendor 读取，测试未访问真实数据库。
- Linux amd64 / arm64 使用 `CGO_ENABLED=0` 构建；二进制及 ZIP 的最终校验记录见构建信息文件。

这些结果是本地验证。真实数据库执行、生产端点响应和客户端刷新后的展示仍需在更新后验证，不能视为已经完成线上更新。

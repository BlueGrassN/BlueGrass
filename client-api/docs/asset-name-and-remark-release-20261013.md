# 同时返回资产名称与客户备注（2026-10-13）

本版替代之前仅修复资产名称的更新包，供现有生产部署替换二进制。程序构建标识为 `20261013-102405-customer-remark`；尚未部署到生产。

## 返回字段

节点列表/详情、资源列表/详情、资源到期摘要均提供两个独立字符串字段，例如：

```json
{
  "id": "11",
  "name": "美国-198.87.96.137",
  "customer_remark": "我的办公节点"
}
```

- `name`：真实资产名称 `proxy_ips.asset_name`，去除首尾空白，不被备注替换，不追加 ID。资产名称缺失时保持既有的中性占位名称。
- `customer_remark`：当前客户订阅的 `subscriptions.customer_remark`，去除首尾空白。NULL、空或纯空白返回 `""`，字段始终存在。
- 待交付/待审核订阅可以返回自己的客户备注，但继续隐藏未经确认的资产名称。
- `kind=asset` 的裸资产没有对应的当前订阅，因此备注返回 `""`。不借用历史或其他客户的订阅，也不读取后台内部的 `subscriptions.remark` / `proxy_ips.remark`。

接口路径与 ID 不变。完整约定见 [API 文档](API.md) 和 [资源契约](RESOURCE_CONTRACT.md)。旧客户端可忽略新增字段；需要展示备注的客户端应读取 `customer_remark`，不能再把 `name` 当作备注。兼容旧服务器时可将字段缺失视为无备注。本次没有修改 Windows / Android 客户端 UI。

## 更新包

- Intel / AMD：[client-api-name-remark-linux-amd64.zip](../dist/client-api-name-remark-linux-amd64.zip)
- ARM64：[client-api-name-remark-linux-arm64.zip](../dist/client-api-name-remark-linux-arm64.zip)
- [构建及校验记录](../dist/client-api-name-remark-BUILD-INFO.json)

每个 ZIP 只有一个根目录文件 `client-api`，不包含配置、密钥、SQL、Nginx 或 systemd 文件。

**停止 `bluegrass-client-api.service` → 覆盖 `/www/wwwroot/client-api.sunip.cc/client-api` 并保持可执行权限 → 启动服务。** 保留原 `config.json`、`token_key` 和会话表；不需要数据库迁移或 Nginx / systemd 修改。

## 验证及数据库操作范围

相对上一版资产名称修复，运行代码仅修改三个文件：DTO 的 `internal/model/model.go`，以及 `internal/source/nodes.go`、`internal/source/resources.go`。在原来已经按当前客户授权的 SELECT 中读取订阅备注，沿用现有扫描列位；不增加查询次数，不改 WHERE 归属条件、JOIN、分页、事务、连接池或会话写入逻辑，不修改业务数据。

回归验证先在不返回备注的旧实现上复现字段缺失（82 个失败测试及子测试），再实现字段：

- 全模块 `go test -race -json -count=1 -timeout 5m ./...`：438 个测试及子测试通过，52 个需要真实 MySQL 的测试跳过，0 失败。
- `go vet ./...` 通过。
- 测试覆盖列表/详情/到期摘要、空与 NULL 备注、中文及首尾空白、资产名称与备注冲突、无名称时独立保留备注、待交付备注和裸资产空备注；HTTP JSON 在有/无备注时均保留字段。
- 清空 `GO_API_TEST_CONFIG`，使用已交付的 vendored 依赖；未访问真实数据库。
- Linux amd64 / arm64 使用 `CGO_ENABLED=0` 构建。打包校验与本地 amd64 版本执行记录保存在构建信息文件中；arm64 未在真实 ARM 机器执行。

以上仅为本地验证，没有连接生产服务器、生产数据库或公共 API，没有提交或推送 GitHub。生产替换后的实际响应需要另行验证。

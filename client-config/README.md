# 四端共用配置：部署、远程切换 API 与回退

## 配置位置

Windows、Android、iOS、macOS 使用同一份 JSON 协议，配置放在独立目录 `client-config/`。

| 用途 | 位置 |
| --- | --- |
| 仓库中的公开配置 | [config.json](config.json) |
| 服务器上的文件 | `<API 站点实际网站根目录>/client-config/config.json` |
| 客户端固定读取地址 | `https://client-api.sunip.vip/client-config/config.json` |
| 当前默认 API 地址 | `https://client-api.sunip.vip/api/client/v1` |

网站根目录以现有站点配置为准，域名变更不要求移动原网站目录。即使以后 API 改到其他域名，固定配置地址及其 HTTPS 证书仍需保持可用。

初始配置如下：

```json
{
  "version": 1,
  "revision": 1,
  "api_base_url": "https://client-api.sunip.vip/api/client/v1"
}
```

`version` 是配置格式版本，当前保持 `1`。`revision` 是发布序号，每次修改 API 地址都必须大于历史已发布的最大值。`api_base_url` 使用 HTTPS 域名及 `/api/client/v1` 路径，不显式填写端口、不加末尾斜杠、账号、查询参数或片段。

## 首次部署

1. 在 API 站点实际网站根目录中创建 `client-config` 文件夹。
2. 将本目录的公开 [config.json](config.json) 上传到该文件夹。它与 Go API 的数据库配置文件是两个不同文件；这里不填写数据库密码、`token_key` 或登录令牌。
3. 让 `/client-config/config.json` 作为静态 JSON 返回，不要求登录，也不重定向。如果该站点其余路径统一反代或返回 404，需要为这个文件添加独立的精确匹配规则。
4. 从客户端电脑验证返回 HTTP 200，正文与发布的 JSON 一致，然后启动已接入此协议的客户端。

Nginx 站点可在**当前 API 域名的 HTTPS `server {}` 内**加入下面的规则。将示例路径替换为实际完整文件路径，仅调整本站点，无需修改全局 `nginx.conf` 或其他网站：

```nginx
location = /client-config/config.json {
    alias /实际网站根目录/client-config/config.json;
    default_type application/json;
    add_header Cache-Control "no-cache" always;
}
```

保存后检查 Nginx 配置并重载；该精确规则只公开共享配置文件，不开放目录列表。若站点前面还有 CDN，应确保此路径不会长期缓存旧内容；配置更新后清除此文件的 CDN 缓存。

在 Windows 上检查公开响应：

```powershell
curl.exe --fail --show-error --max-time 10 --include "https://client-api.sunip.vip/client-config/config.json"
```

应直接返回 `200`、JSON 正文及 JSON 内容类型。`404` 表示路径或文件尚未发布正确，`301/302` 表示存在客户端不会跟随的重定向。仅生成本地文件或重新打包客户端，不会自动完成服务器发布。

## 远程切换 API 域名

1. 先确认新域名的 HTTPS 证书、`/api/client/v1/health` 和账号登录／资源接口正常，继续保持固定配置地址可访问。
2. 修改公开配置的 `api_base_url`，同时增加 `revision`。例如原序号是 `1`，下一次可使用 `2`；下方新域名仅为示例，应替换为实际域名。
3. 完整替换服务器上的 `client-config/config.json`，如有 CDN 则清除此文件缓存。重新执行上面的检查命令，确认读到新地址和新序号。
4. 完整退出客户端后重新启动，再登录并检查资源列表与连接。Windows 点击窗口“×”只隐藏到托盘，应使用托盘菜单“退出”后再打开。

```json
{
  "version": 1,
  "revision": 2,
  "api_base_url": "https://api-next.sunip.vip/api/client/v1"
}
```

API 地址变化后需要重新登录，旧域名的 access/refresh token 不会发送到新域名。配置在进程启动后、恢复登录前读取一次，当前连接不会中途切换。

## 回退到原 API

把 `api_base_url` 改回原地址，并继续增加 `revision`。例如已经发布序号 `2`，回退应发布 `3`，不能直接重新上传序号 `1` 的旧文件：

```json
{
  "version": 1,
  "revision": 3,
  "api_base_url": "https://client-api.sunip.vip/api/client/v1"
}
```

发布、缓存刷新和客户端重启步骤与切换相同。客户端拒绝更旧的序号，也不会接受同一序号对应另一个 API 地址。

## 缓存与接入范围

Windows 拉取配置的上限为 5 秒、4 KiB。404、超时、TLS 错误、错误格式或过旧序号不会替换有效缓存；首次没有缓存时使用编译默认 API。配置获取失败时沿用的是有效配置缓存，不代表已检查该 API 此刻可用。

受保护的 TUN 会话尚未恢复时只读取本地缓存，不通过普通网络绕开保护拉取配置。主动恢复网络后，完整退出再启动，才会重新拉取远程配置。

截至 2026-10-15，Windows `0.1.0-beta.1+11` 已接入；其他三端共用此协议，但需要各自实现配置读取才会生效。本次迁移验收时公开配置地址返回 404，不能将本地文档或打包完成当作服务器已经发布。迁移、测试与交付记录见 [API 迁移验证](../Windows/docs/api-migration-20261015.md)。

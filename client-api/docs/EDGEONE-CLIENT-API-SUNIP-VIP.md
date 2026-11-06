# client-api.sunip.vip：使用 EO 客户 IP

本站 Nginx 优先使用 `EO-Connecting-IP`，头部缺失或为空时使用实际连接 IP。这个地址统一用于访问日志、按 IP 请求/连接限流，以及通过 `X-Real-IP` 传给 Go 记录新登录会话的 `login_ip`。既有会话的历史登录 IP 不会改写。

## 使用方法

**按顺序保存：先站点配置，再反代配置。** 宝塔会在每次保存时检查整个 Nginx 配置。

| 文件 | 替换位置 |
| --- | --- |
| `client-api.sunip.vip.nginx-site.conf` | `/www/server/panel/vhost/nginx/client-api.sunip.vip.conf` |
| `client-api.sunip.vip.nginx-proxy.conf` | `/www/server/panel/vhost/nginx/proxy/client-api.sunip.vip/` 内原有反代文件的内容，保留原文件名 |

1. 用上面的站点文件完整替换本站配置并保存。文件同时声明旧限流区域和新的 EO 限流区域，旧反代尚未替换时也能校验通过。
2. 再用上面的反代文件替换原有反代配置并保存，客户 IP 和按 IP 限流随之切换到 EO 地址。
3. 如宝塔没有自动重载，使用宝塔的 Nginx 重载功能；无需重启 Go 服务。

出现 `zero size shared memory zone "client_api_sunip_vip_ip"` 表示仍有旧反代引用该区域，而站点文件没有声明它。使用本说明配套的最新站点文件先保存即可。不要只把新限流区域改回旧名字：原区域使用 `$binary_remote_addr`，更换其 key 也会导致重载失败。

两份文件配套使用。`map`、限流区域与日志格式仍放在本站配置的 `server {}` 外，不需要修改 Nginx 主配置或其他站点。Go 二进制、systemd、数据库、config.json 都无需调整。

EO 已默认提供 `EO-Connecting-IP`，无需添加回源密钥或自定义请求头。API 域名继续保持不缓存、HTTPS 回源 443，回源 Host/SNI 为 `client-api.sunip.vip`。

访问日志第一列为选定的客户 IP，`peer=` 为与 Nginx 建连的地址。Go 继续只信任本机 Nginx 的 `X-Real-IP`，不读取任意 X-Forwarded-For 链。

这份配置按要求直接信任 EO 头，不验证回源来源；能够直连源站的人仍可通过伪造该头影响 IP 记录和限流。

## App 的公开 API 地址配置

反代文件通过精确匹配开放 `https://client-api.sunip.vip/client-config/config.json`，对应网站文件 `/www/wwwroot/client-api.sunip.vip/client-config/config.json`，以 JSON 返回并设置 `Cache-Control: no-store`。文件必须实际存在且 Nginx 用户可读；文件不存在仍返回 404。

此次放通只需在宝塔「本站 → 反向代理 → 原有代理 → 配置文件」替换最新反代文件并保存，站点主配置无需再次替换。这个公开文件只放 App 的 API 地址配置，不要将网站根目录中包含数据库密码和 token_key 的私有 `config.json` 复制到此目录。其他文件和目录仍被阻断。

EO 对这个路径也应设置节点不缓存、浏览器缓存遵循源站；如之前缓存了 404，在 EO 刷新该 URL。客户端配置字段为 `version`、`revision`、`api_base_url`，修改 API 地址时需增加 `revision`；具体格式以客户端 `Windows/app/lib/api_endpoint.dart` 为准。

## 本地验证

`tests/edgeone_proxy_test.py` 使用真实 Nginx 和本机模拟上游验证先保存站点再替换反代的混合状态、IPv4/IPv6、无 EO 头时的回退、公开 App 配置读取与更新、私有文件阻断、日志及不同客户的限流隔离，不访问生产环境或数据库。完整宝塔配置的证书、扩展 include 和 HTTP/3 仍需部署时校验。

```text
python tests/edgeone_proxy_test.py --nginx /path/to/nginx
```

[EO 默认 HTTP 回源请求头官方说明](https://cloud.tencent.com/document/product/1552/87654)

# Hytron 测试 API 运行记录

2026-10-11，Asia/Hong_Kong。用户完成宝塔反代后明确要求启动 API，本记录更新最初“仅放置代码、未启动服务”的交付状态。

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

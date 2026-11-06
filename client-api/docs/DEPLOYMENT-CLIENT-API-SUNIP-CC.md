# client-api.sunip.cc 简单部署教程

直接在 `/www/wwwroot/client-api.sunip.cc` 放程序和配置，宝塔 Nginx 负责反代，systemd 负责运行。无需安装 Go 编译器，不改宝塔网站目录、运行目录或 SSL，不使用版本目录。本教程不列 shell 命令。

默认数据库账号和三张 API 表已经准备好。全程使用宝塔文件管理器编辑和移动文件即可；启动服务通过你现有的 systemd 服务管理方式完成。

## 1. 只修改 client-api.sunip.cc 这一个站点

在宝塔「网站 → client-api.sunip.cc → 设置 → 配置文件」编辑该域名自己的配置，通常是：

`/www/server/panel/vhost/nginx/client-api.sunip.cc.conf`

**不修改 Nginx 主配置，不修改其他域名或 API 的配置，不覆盖整份站点文件。** 包里的两份片段都放进上面同一个文件：

- `nginx-site-prefix.conf`：添加到文件最上方，放在所有 `server {}` 的外面，只添加一次。
- `nginx-server.conf`：合并到这个域名已有的 HTTPS `server {}` 内，替换对应的旧反代和 `location /`，同名设置只留一份。保留这个站点的域名、SSL 和证书续期规则；需要移除的旧 PHP、伪静态规则也仅限这个站点。

宝塔默认将站点文件包含在 `http {}` 中，所以限流声明可以写在站点文件的 `server {}` 外面，无需去修改主配置。声明名称都带 `client_api_sunip_cc` 前缀，只有本 API 的反代引用它们；其他站点不会因此新增限流。如果你的站点文件被自定义为嵌套在 `server {}` 内，不能照搬第一份片段，需要按实际包含结构调整。

在宝塔配置编辑器中保存并应用，若提示语法错误先处理错误。Nginx 应用配置时会重新加载共享配置；这里限定的是只编辑本站点，不能把重新加载说成独立启动一个 Nginx。

模板已将 `/api/client/v1/` 转发到 `http://127.0.0.1:8087`，保留完整路径，并设置真实 IP、HTTPS 转发头和请求限流。`/config`、`/client-api` 开头的文件路径禁止下载。先应用这份配置，再把部署包上传到网站目录。

## 2. 上传并解压

普通 Intel / AMD 服务器使用 `client-api-linux-amd64.zip`；ARM64 服务器使用 `client-api-linux-arm64.zip`。

在宝塔文件管理器里，将对应 ZIP 上传到 `/www/wwwroot/client-api.sunip.cc`，直接解压到当前目录，不额外套一层目录。解压后包括：

| 文件 | 用途 |
| --- | --- |
| `client-api` | Go 可执行程序 |
| `config.json` | 数据库与 API 配置 |
| `bluegrass-client-api.service` | systemd 服务文件，下一步移到系统服务目录 |
| `nginx-site-prefix.conf`、`nginx-server.conf` | 都用于本域名同一站点文件的配置片段 |
| `部署说明.md` | 本教程 |

## 3. 编辑 config.json

在宝塔文件管理器中打开 `/www/wwwroot/client-api.sunip.cc/config.json`。只需要把 `source.password` 和 `sessions.password` 中的 `REPLACE_DATABASE_PASSWORD` 都替换成同一个 `v2_test` 密码。

以下内容已填好：

| 配置 | 值 |
| --- | --- |
| API 监听 | `127.0.0.1:8087` |
| 数据库地址 | `127.0.0.1:3306` |
| 数据库名 | `api_all_sunip_cc` |
| 两处数据库用户名 | `v2_test` |
| `token_key` | 已为这次部署生成随机密钥，不需要另行生成 |

如果服务器已有其他服务占用 8087，为本 API 选一个未使用的本机端口，同时修改 `config.json` 的 `listen` 和本站点 `proxy_pass` 的端口，不停止或覆盖其他 API。

保持 JSON 格式；密码只填在服务器私有配置中。不要更改 `source`、`sessions` 这些键名。以后更新程序保留这份配置及 `token_key`。

在宝塔的文件权限设置中：`client-api` 权限设为 **755**；`config.json` 所有者设为 **www**、权限设为 **600**。网站目录需允许 www 用户进入；宝塔默认的 www 用户用于下面的服务文件，不另建用户。

## 4. 放置并启动 systemd 服务

将包内 `bluegrass-client-api.service` 移到：

`/etc/systemd/system/bluegrass-client-api.service`

文件所有者 root、权限 644。文件已写好，直接使用，其程序和配置路径固定为：

| 项目 | 值 |
| --- | --- |
| 运行用户 | `www` |
| 工作目录 | `/www/wwwroot/client-api.sunip.cc` |
| 程序 | `/www/wwwroot/client-api.sunip.cc/client-api` |
| 配置 | `/www/wwwroot/client-api.sunip.cc/config.json` |
| 服务名 | `bluegrass-client-api.service` |

通过你现有的 systemd 服务管理工具重新加载服务定义，为这个服务设置开机自启并启动。复制 `.service` 文件本身不会自动启动程序；如果管理工具不支持 systemd 加载或启动，仍需由服务器管理员完成这一步。这里不要求再配置宝塔的第二个进程守护。

启动后，在浏览器打开 `https://client-api.sunip.cc/api/client/v1/health`，响应中 `data.status` 为 `ok` 表示程序和反代可达；这不代表全部数据库业务已经通过验证。

## 5. 以后更新

**停止 `bluegrass-client-api.service` → 覆盖网站根目录里的 `client-api` → 启动服务。**

保持文件可执行权限，保留原 `config.json`。不要用新包里的配置覆盖现有密码和 `token_key`；无需改路径、建立版本目录或重新填写 systemd。

本包仅供本次私有部署，配置包含服务密钥，不公开分享。本文及新包只在本地生成，没有连接生产环境，也没有实际执行安装或启动。

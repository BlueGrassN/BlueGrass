# BlueGrass 共享原生网络与控制模块

模块名继续为 `bluegrass/native/core`，本目录由多端共享；平台专用实现按 build tags 和宿主适配隔离。`macproxy/`、`mactun/`、`mactunipc/` 为 macOS 系统代理、TUN 和授权 IPC 实现。

当前代理内核为 Xray-core v26.10.17，固定 Go 模块版本为 `v1.260327.1-0.20261017152356-92ccc939aaf4`，源码提交 `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`。上游将该版本标记为预发布；这里记录具体 pin，不使用浮动 latest。

- `protocolconfig/` 校验封闭的版本 1 资源协议字段并生成 Xray 出站，拥有会话级 TCP/UDP、取消和关闭语义。
- `localengine/` 提供固定 `127.0.0.1:12808` HTTP 网关。端口占用明确报错，不换随机端口。无参数原生宿主用于隔离测试；`--system-proxy` 配合 `winproxy/` 管理系统代理设置及恢复。
- `tunengine/` 使用 Xray 原生 TUN 网卡与 TCP/UDP 协议栈（`proxy/tun.NewTun` / `NewStack`）及 Xray 代理出站。Windows 构建不包含 sing-box、sing-tun 或 gVisor。sing 的地址/缓冲区等通用工具和本地缓存库不属于代理或 TUN 内核。
- `winservice/`、`winprotect/` 和 `nativehost/` 分别承担提权服务组合、WFP 归属和可信宿主边界；实际 OS 保护状态以平台返回的结果为准。
- `control/`、`engine/`、`entrydns/`、`exitprobe/`、`diagnostic/` 等目录负责控制状态、网络策略和可观测性。测试账号、节点认证信息、令牌不进入配置快照或诊断输出。

## 连接与资源

支持 SOCKS5、HTTP/HTTPS、Shadowsocks、VMess、VLESS、Trojan 和 Hysteria2，具体字段见[协议矩阵](../docs/development/xray-protocol-matrix.md)。TUIC、AnyTLS 的资源记录可以解析与展示，但固定 Xray 版本不支持连接，返回 `XRAY_UNSUPPORTED_PROTOCOL`，不回退其他核心。

连接前核对当前资源身份、版本、入口、协议字段及已知到期时间。有效配置准备完成后打开业务转发，再通过所选出站发起 HTTPS 出口观测。`expectedExitIpv4` 为可选元数据；缺失、变化或观测失败都不作为连接门槛。界面在尚未观测时显示“暂未获取”。

每次准备创建新会话，暂停、切换、到期和关闭取消旧会话、关闭旧流；迟到观测只允许更新所属会话。业务门是应用内部状态，不能单独证明系统代理、TUN 或 WFP 已经生效。TUN 保留本地网络/本地域名策略、DNS 路由、物理入口绑定和经过认证的 SOCKS UDP 中继权限。

## 开发与验证

使用 Go 1.27 或更新的兼容工具链。Xray 原生 TUN 不需要额外构建标签。Windows 发行包必须包含校验后的官方 Wintun DLL；服务仍独占 WFP，Xray 只配置自身网卡的路由与 DNS，不启用全局自动出口控制器。

```powershell
go test -p 1 -count=1 ./...
go test -p 1 -race -count=1 ./...
go vet -p 2 ./...
```

Windows 原生及完整客户端构建入口分别是 `../Windows/scripts/build-native.ps1` 和 `../Windows/scripts/build.ps1`；macOS 使用 `../Mac/tools/build.py`，Android 原生桥接位于 `../Android/native/core`。发布校验须检查最终 EXE 的 `go version -m`：两个网络宿主必须包含固定 Xray 模块，不能包含 `github.com/sagernet/sing-box`、`github.com/sagernet/sing-tun` 或 `github.com/sagernet/gvisor`。

本目录测试包含受控 TCP/UDP 服务器和平台替身。通过这些测试不代表提权 TUN/WFP、休眠恢复或真实线路已通过；当前证据应查看对应验证记录。旧 `system-proxy-verification.md` 等报告保留其原始版本/日期，不能作为新内核验收。

完整平台规则见 [Windows 开发手册](../docs/development/windows.md)及[四端共享手册](../docs/development/four-platform-development.md)。

本次原生 TUN 迁移与验证见 [原生 TUN 报告](../Windows/docs/xray-native-tun-verification.md)。同一源 UDP socket 的每个目的地址独立经过 DNS/路由/权限判断，不能沿用首包目标；关闭会话先取消在途流，再关闭栈和网卡并确认路由/DNS 清理。

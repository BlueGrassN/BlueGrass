# 指定资源的真实连接验证

这是 2026-10-11 的历史记录。2026-10-12 已接入并验证真实系统代理，最新包和测试结果以 [系统代理验证](system-proxy-verification.md) 为准；下文关于“不修改系统代理”的范围仅适用于当时的独立本地代理版本。

日期：2026-10-11，Windows x64。账号来自仓库根 `testaccount.txt`，未在日志、源码或包内记录账号密码。预期出口由用户明确给定为 `63.161.180.219`，不是根据探测结果推断。

## 已取得的真实证据

- 专用 API 登录成功；该账号本次返回 3 个可连接节点。
- 分别经 SOCKS5 中转发送 HTTPS IPv4 探测，唯一匹配的订阅是 `11134`；另外两条结果不匹配，不将它们绑定到该 IP。
- 内嵌 sing-box 1.14.2 的 Go 宿主实测：故意使用不同预期出口时拒绝开门；使用指定预期时连接成功。
- 通过原生进程在独立回环端口提供的 HTTP CONNECT 再次访问检测接口，得到 `63.161.180.219`；真实下载字节计数大于零。
- 暂停后业务请求被拒绝；Disconnect 确认关闭门，EOF 后子进程退出 0。
- Flutter Windows 实机集成已完成：真实账号登录 → 选择绑定资源 → NativeClient 私有管道 → Go/sing-box 出口验证 → 本地代理 HTTPS 请求 → 流量统计 → 断开 → 登出。测试使用独立设备 ID 和专用测试会话存储。

检测服务为 [ipify IPv4 HTTPS API](https://www.ipify.org/)，请求地址 `https://api.ipify.org?format=json`。探测不携带客户 API Token、账号标识或中转凭据；TLS 使用系统信任且不跳过证书校验。重定向、非 200、非公网 IPv4、额外/过大响应及出口不匹配均不能开门。

## 现用代理保持原状

`127.0.0.1:10808` 是用户现用服务。本轮没有对它执行停止、重启、修改或绑定操作。只读检查先观察到监听 PID `7080`，后续为 `29780`；没有控制这两个进程，ProxyServer 始终为 `127.0.0.1:10808`。

ProxyEnable 的阶段性读数是 0、1、0。运行宿主不引用 winproxy 包，调用路径不存在系统代理写入；winproxy 的回归只注入 fake WinInet。没有将这些外部变化归因于测试，也没有恢复旧快照，始终保留读取时的最新状态。API 请求在明确参数下借用现有代理进行 HTTPS 通信，不改变该代理。

BlueGrass 测试宿主只在 `127.0.0.1:0` 分配新端口，界面显示实际本地 HTTP 地址；`systemProxyApplied` 明确为 false。本轮没有设置系统代理、WinHTTP、路由、防火墙或系统服务。

## 复现命令

在仓库根目录执行，输出仅有白名单化结果：

```powershell
./Windows/scripts/build-native.ps1
python Windows/scripts/live-socks-check.py --account-file testaccount.txt --expected-ip 63.161.180.219 --api-proxy http://127.0.0.1:10808 --binding Windows/.artifacts/live-binding.private.json --native-executable Windows/native/bin/BlueGrass.Native.exe
./Windows/scripts/test-live.ps1 -ExpectedIp 63.161.180.219 -ApiProxy http://127.0.0.1:10808
./Windows/scripts/check.ps1 -NativeIntegration
./Windows/scripts/build.ps1 -LiveTest -ApiProxy http://127.0.0.1:10808
```

测试绑定只包含环境、账号 ID、订阅 ID、指定出口和公共检测地址。普通构建不启用绑定文件；测试构建也只在完整资源身份匹配时使用它，与 API 的非空预期值冲突时拒绝。

## 审阅与简化

独立审阅的复现已转为回归：错误 runtime/schema/账号/attempt 不能确认控制状态或 Disconnect；合法输入不能放大成超限回包；暂停关闭普通 HTTP 上传连接；TCP 半关闭保留上游返回。额外拒绝向暂停命令夹带旧 profile，避免看似指定旧资源却作用于当前资源。

单纯转发 `context.Background()` 的包装函数已删除，删除前后两包 15 项功能回归一致。独立副本内移除迟到拨号检查，会触发 `late dial escaped closed gate`；恢复后回归通过。生产源码没有保留消融开关。

## 限制

已验证的是该资源的 TCP、本地 HTTP/CONNECT 及初始固定出口验证。未验收全局系统代理接管、TUN/WFP、特权 Windows 服务、UDP、IPv6、跨重启恢复、多用户隔离或零泄漏。当前 API 仍缺固定出口字段，通用资源不会自动获得本次测试覆盖。`winproxy/` 代码未启用，不能根据 fake 测试声称已通过真实接管验收。

原始结果在 `Windows/.artifacts/verification/live-windows-integration.log` 及最终检查日志；内核消融结果在 `Windows/.artifacts/ablation/native-gate/`。

## 修复后的最终检查

- 领域 73 项、API 70 项、Flutter/IPC 98 项通过；三处 Dart 静态分析无问题。
- Go 84 个顶层测试通过 race，vet 通过；包含 28 项只使用 fake WinInet 的代理事务测试。
- 契约校验 8 项、Windows DPAPI/模拟桌面集成 2 项通过。
- 修复身份校验后，再次运行真实 Windows 指定资源测试，1 项通过；完整连接/请求/断开/登出均执行。
- Flutter 重复构建曾被导入许可证的只读属性阻止，已修正源文件和生成副本属性；之后实机集成构建通过。没有通过跳过测试来掩盖构建失败。

## 当前交付包

两个包均包含 UI、`BlueGrass.Native.exe`、Flutter 数据/运行库、VC++ CRT 和 sing-box 许可；已检查包内不含 testaccount.txt 或会话 DPAPI 文件。

| 文件 | 字节数 | SHA-256 |
| --- | ---: | --- |
| `dist/BlueGrass-Windows-x64-live-test.zip` | 17062132 | `AF4958224CA12CE806FEE9E9AA25E74C10C0FEDA4E5A312FE1DF6280C89A8F36` |
| `dist/BlueGrass-Windows-x64-dev.zip` | 17055436 | `22A0902CED6D4C2CBADCF10D3D3A8992C9A852CC21E1DA5E6B8094C81B624E14` |

测试包启用当前账号/订阅限定的出口绑定与显式 API 代理，普通包未包含/启用该绑定。两者均只支持独立本地代理，不自动写入系统代理。

已启动最终 Release 测试包，确认登录页正常显示“测试连接 / 独立本地代理”标识，证明绑定文件及构建开关实际生效。启动未登录、未连接任何线路；窗口保留供本机查看。最后只读检查仍是 xray 监听 10808、ProxyServer=127.0.0.1:10808、ProxyEnable=0，未执行任何恢复或重启操作。

# Windows 路由模式验证

日期：2026-10-21。范围仅 Windows，目标版本 `0.1.0-beta.1+19`。使用方式见[路由模式说明](routing-modes.md)。

## 功能与检查

路由选择为“国内外分流 / 全局代理”，TUN 在连接操作旁独立开关。系统代理与 TUN 使用同一份固定分类数据，出口显示始终检测所选节点。已有偏好兼容全局，新安装默认分流；连接中禁止修改两项模式。

- Flutter：381 项测试通过、静态分析无问题；最终出口标签修改的 12 项界面回归通过。日志为 `.artifacts/verification/routing-flutter-tests.log`、`routing-flutter-analyze.log`、`routing-ui-final.log`。
- 跨语言契约：13 项通过，见 `routing-contracts.log`。
- 最终原生回归：`go test -p 1 -race -count=1 ./...` 全部通过，`go vet -p 2 ./...` 退出码 0；见 `routing-complete-race.log`、`routing-complete-vet.log`。
- 真实系统代理数据面：从完整列表选取同一可用节点，两种模式均通过国内/国外出口比较、统计、暂停阻断和退出清理。`routing-system-live-same-dns.jsonl` 记录全部结果。直接基线使用与分流一致的国内 DNS 和 IPv4，避免把不同 CDN 地址的出口差异误判为路由错误；测试没有改动系统代理设置。

## 管理员实机验证

验收可执行文件包含真实生产 TUN、Xray、WFP 组件；业务探测从另一可执行路径发起，不享有服务程序的出口例外。独立看护进程先于任何网络修改启动。凭据只在内存使用，日志仅记录序号、数量、布尔值和固定错误码。

分流完整通过记录：`.artifacts/verification/elevated-tun/runs/11ee92f6c26044058878ebd0392f4780/summary.json` 和同目录 `events.jsonl`。

| 分流实机项目 | 结果 |
| --- | --- |
| 国外 HTTPS / UDP STUN | 与所选节点出口一致 |
| 国内字面 IPv4 / 国内域名 | 与物理网络基线一致，与节点出口可区分 |
| 域名连接 | 实际使用 Windows DNS 返回的 FakeIP，再恢复原域名分类 |
| DNS | UDP、TCP、Windows 系统解析均进入 FakeIP 策略 |
| 暂停 / 恢复 | 国内外新连接均被阻断；恢复后重新验证对应出口 |
| 显式断开 | WFP 删除，物理 HTTPS 和系统 DNS 恢复 |
| 工作进程异常退出 | 刻意退出码 92，WFP 保留阻断；独立崩溃检查、修复退出码均为 0 |
| 主机恢复 | 路由、DNS、系统代理、10808 监听身份与测试前一致 |

全局模式最终记录为 `.artifacts/verification/elevated-tun/runs/6a2582c163ba457c92e5a2c207ed9372/`：国内字面 IP、国内域名与国外 HTTPS 均经所选节点，域名使用 FakeIP；TCP/UDP/系统 DNS、UDP STUN、暂停恢复、显式断开、异常退出阻断、独立恢复全部通过。追加三轮连续“准备、激活、暂停、释放”也全部通过。退出码为 `92 / 0 / 0`，前后网络比较全部为 true，10808 仍由原 PID 45408 监听。

分流完整验收在最后两项共享生命周期修正前完成；修正后由原生完整回归及上述全局连续启停实机验收覆盖。没有将历史的单次通过掩盖为每个构建都重复执行全部场景。

## 最终产物

- 版本：`0.1.0-beta.1+19`，启动器与 GUI 的 Windows 版本字段一致。
- ZIP：`dist/BlueGrass-Windows-x64-dev.zip`，57,789,601 字节，46 个文件；SHA256 为 `09b40d084ff854c50c51300db0d6846ffffdb53be8743ff8f093a94ca85c6991`。
- 已删除旧的固定解压目录并解压新包，根仅保留 `BlueGrass.exe` 和 `runtime/`。ZIP 与解压文件逐字节一致；规则、许可证、Xray 模块、官方签名 Wintun、品牌图标及生产 API 均验证通过。
- 从 `C:/Windows` 工作目录通过根启动器启动，实际 GUI 为 `runtime/BlueGrass.exe`，主窗口标题 `BlueGrass`、版本 +19，系统代理没有变化。新版窗口已打开供查看。
- 包内 Native Hello/GetStatus、诊断助手、12808 释放、更新配置边界检查通过。日志为 `routing-complete-build.log`、`routing-complete-bundle.log`、`routing-complete-portable.log`、`routing-complete-window.json`。

更新解压前会检查旧目录是否有正在运行的客户端进程，若有则在删除任何文件前停止打包展开步骤；关闭旧客户端后再执行。不会为覆盖包而自动终止用户运行中的客户端。

## 实机问题与精简验证

每条直连 socket 原先都创建通用网卡查找器，首次查询会枚举全部网卡及其地址。在真实 TUN 并发流量下，诊断运行 `5babbb8441294702a0517e7af9276946` 测得一次绑定耗时 3819ms。改为只查询指定网卡、传入确定索引，失败仍拒绝连接；运行 `666628dc018449d48b73adb93518ed19` 中同一绑定调用记录为 0ms，物理源地址与出口比较通过，随后完整分流验收通过。这是该环境的单次诊断观测，不是通用性能倍率。

WFP 更新保留未变化过滤器的 GUID，只增删本次需要变更的精确规则；首次启动或事务失败后仍从系统重新核对所属规则。普通端点许可、暂停阻断及恢复边界不变。Windows 仍可能因过滤器变化重新授权已有流，不能宣称增量更新完全消除重新授权。

STUN 验收由单发改为在总时限内按退避重传同一事务，避免把一次 UDP 丢包当作内核故障；本地回归主动丢弃首包并核对重传事务 ID 和回复。真实节点的独立 UDP 基线也已通过。临时逐连接跟踪和人工预授予直连许可已移除，完整验收使用正常业务路径。

连续启停扩展检查发现 Windows DNS 删除的异步收敛和本地运行时过短的网络读取时限。原生关闭后只读复查所属路由、地址和 DNS 最多 3 秒，等待期间保持 WFP；不重复原生删除，超时仍保留失败。网络快照上限由 1 秒改为 3 秒，仍检查相同物理网络代次并响应调用取消。`TestCheckedNativeCloseWaitsForDNSRemoval` 和 `TestLocalOnlySlowUnchangedNetworkSnapshotDoesNotRetire` 在修正前失败、修正后通过；已有未知所有权、持续残留、取消和真实网络变化拒绝测试继续通过。内部错误保留原因，公开错误文本仍使用固定代码。

## 验证边界

本机 IPv6 和网关 HTTP 没有可用的对照基线，所以没有将 IPv6/LAN 实机项目计入通过。国内普通 UDP 直连由路由/socket 回归覆盖，实机 STUN 验证的是国外 UDP 经节点。管理员组件测试不等于签名 SCM 服务安装或 GUI 到已安装服务的完整安装链验收；没有执行重启、休眠、多用户或正式签名安装卸载。便携包仍未签名，自动更新需要正式签名与配置。

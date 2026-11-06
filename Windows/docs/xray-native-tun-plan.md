# Windows Xray 原生 TUN 迁移

2026-10-20。用户已要求将 Windows 整体切换为 Xray；Android 在另一个会话处理。

## 实现边界

- 保持锁定的 Xray-core v26.10.17。使用其 `proxy/tun.NewTun` 和 `NewStack` 接管虚拟网卡及 TCP/UDP 数据包，删除 sing-tun/gVisor 运行时。
- 系统代理和全局接管均使用已有 Xray 协议出站；保留客户端会话门控、WFP、局域网/DNS/FakeIP 策略和出口观测。缺少预期出口 IP 不阻止连接。
- 服务独占 WFP 生命周期，Xray TUN 只配置其拥有的网卡、路由和 DNS。物理出口继续显式绑定，不注册跨会话的全局自动出口控制器。
- 从 Xray 网卡接口索引转换 LUID。兼容旧版 BlueGrass 网卡标识，修复只删除经名称、GUID、驱动身份验证的本产品网卡。
- TCP/UDP、失败回滚、取消、重连和网络恢复必须验证。使用 Xray 内存协议栈完成真实包级测试；提权实机验证与自动测试结果分开记录。
- Windows 包提供已校验的官方 Wintun DLL 和许可证；根目录仍只有启动程序和 runtime。保留固定 HTTP 端口 12808，禁止修改用户的 10808 服务。

## 执行与验收

1. 先添加/迁移原生启动和数据包测试，确认旧实现不满足新契约。
2. 替换网卡、协议栈和入站适配；将 UDP 每包目的地址保留下来，避免同一源端口访问多目的地时混淆。
3. 同步驱动打包、依赖守卫和修复工具。删除已不再使用的代码和构建标签。
4. 运行 Go 全套测试、race、vet，Flutter 回归和分析，检查真实打包依赖；用消融验证关键取消/UDP 隔离检查的作用。
5. 构建新的 beta，删除 dist 中旧的固定解压目录后解压新包，验证归档一致性并更新手册及验证报告。

来源：锁定版本的 Xray 本地源码、https://xtls.github.io/en/config/inbounds/tun.html 与 https://www.wintun.net/ 。

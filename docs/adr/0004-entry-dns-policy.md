# ADR 0004：入口解析优先使用内核，并支持指定 DNS

- 状态：已接受。
- 决策日期：2026-10-10，Asia/Hong_Kong。
- 对应事项：设计审阅报告 D-04。
- 确认依据：用户选择 A，并明确要求能够指定 DNS 解析。

## 决定

1. 入口解析优先使用 sing-box；App 管理解析策略，Go/Kotlin 原生层负责平台网络绑定，并补足经 S8 验证确认的容错或诊断缺口。
2. 设置页提供“入口 DNS”的自动与自定义模式。自定义模式允许指定一个或多个 DNS 服务器，以列表顺序表达首选和备用；地址使用 IPv4/IPv6 字面量，端口默认 53，支持 UDP/TCP 配置。
3. 自动模式使用产品默认候选，可包含物理网络 DNS 和不同提供商的公共 DNS；自定义模式只在用户配置的列表内查询和切换，不隐式加入系统 DNS 或产品默认 DNS。
4. 自定义配置持久化，并优先于远程配置提供的默认值；用户主动恢复自动模式后再采用默认候选。
5. DNS 全部失败时明确报错，保持已有断线保护。单个指定 DNS 可用时正常查询；配置了备用 DNS 时按超时、错误类别和优先级切换或延迟并发，不把地址列表本身视为已实现容错。

## 适用范围与网络路径

- 本项指定的是中转入口域名的 DNS。解析请求由受控原生组件经当前物理网口、Wi-Fi 或移动数据发出，避免经过自己的 TUN/VPN。
- 业务域名继续经所选代理线路处理，不使用入口 DNS 设置修改系统 DNS 或为普通应用增加直连 DNS 豁免。
- Windows 保护规则仅放行受控服务访问当前策略允许的 DNS 地址、端口和传输协议；Android 通过底层网络绑定和 `protect` 配合实现。
- TLS 类协议保留入口原始域名，用于 SNI、Host 和证书验证；指定 DNS 不影响证书校验要求。
- DoH 及其自举仍作为后续扩展，本项先明确普通 DNS 的服务器选择能力。

## 配置与生效

- `dnsMode` 为 `auto` 或 `custom`；`dnsCandidates` 为有序的结构化列表，每项包含 `address`、`port` 和 `transport`。
- 用户修改后保存偏好，并通过受控接口更新原生层 DNS 策略；清理旧策略的入口缓存，下一次入口解析使用新配置，已建立的代理连接不因修改 DNS 主动中断。
- 配置及保护规则更新失败时保留原策略并显示错误；成功后诊断中可查看当前模式、实际使用的 DNS 和失败类别。

## 验证与影响

S8 验证指定单服务器、多服务器切换、全部失效、自定义模式不调用列表外 DNS、设置持久化、运行中策略更新、网络切换与物理网络绑定。内核不能满足的部分由原生层补齐，验证结论记录具体版本和实现方式。

## 官方依据

sing-box 的 [domain_resolver](https://sing-box.sagernet.org/configuration/shared/dial/#domain_resolver) 可以选择入口域名使用的解析器；[UDP DNS](https://sing-box.sagernet.org/configuration/dns/server/udp/)和 [TCP DNS](https://sing-box.sagernet.org/configuration/dns/server/tcp/)提供服务器配置。具体配置格式与故障切换行为在 S8 按锁定版本验证。

## 关联文档

- [主设计](../superpowers/specs/2026-10-10-sunipip-client-design.md)
- [设计审阅报告](../superpowers/reviews/2026-10-10-sunipip-client-design-review.md)
- [补充设计](../superpowers/specs/2026-10-10-sunipip-client-design-addendum.md)
- [实施计划](../superpowers/plans/2026-10-10-sunipip-client-implementation-plan.md)

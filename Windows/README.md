# BlueGrass Windows

下载统一使用 [开发预览版 Release](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)，各端安装包集中展示并独立更新；规则见 [共享发布手册](../docs/development/github-releases.md)。

2026-11-06：新增[本地构建与 GitHub Release 流程](docs/github-release.md)。本地运行 `./Windows/scripts/package.ps1` 生成并解压测试包；Windows 相关代码推送到 main 后，GitHub 自动检查、构建并发布预发布 Release。

2026-11-05 交互更新：首页“选择资源”改为可搜索的就地弹出面板。新版入口与验证记录见 [首页资源选择](docs/home-resource-picker.md)。

2026-11-05：Windows 界面整体改为 FlClash v0.9.2 风格，保留 BlueGrass 功能与品牌。当前便携包为 **0.1.0-beta.1+21**；包含新仪表盘、可收起侧栏、窄窗口导航、资源卡片和统一的浅色/深色页面。运行入口、截图、备份与验收记录见 [FlClash 风格界面改版](docs/flclash-ui-redesign.md)。

2026-10-19 内核要求：按 [ADR 0049](../docs/adr/0049-xray-only-four-platforms.md)，四端唯一代理内核改为 **Xray-core v26.10.17（上游预发布）**，禁止 sing-box 回退或第二代理内核。参见 [Windows 开发手册](../docs/development/windows.md)、[四端共享手册](../docs/development/four-platform-development.md)与[协议矩阵](../docs/development/xray-protocol-matrix.md)。Go 服务、`127.0.0.1:12808` 与 WFP 保护/恢复语义继续保留。

2026-10-19 内核验收版本：`0.1.0-beta.1+17`。代理协议、TUN 网卡和 TCP/UDP 栈均使用 Xray；sing-tun/gVisor 已移除。构建、回归、驱动与真实线路结果见 [本次记录](docs/xray-native-tun-verification.md)。Android 在另一会话继续，本次仅交付 Windows。

2026-10-19 的 Go race/vet、Flutter 回归、包级 Xray TCP/UDP、驱动加载及便携包检查通过；5 条真实线路中 4 条通过，1 条由独立对照确认上游认证拒绝。管理员权限下的真实 TUN/WFP、重启和签名安装仍需独立验收，不能从内存协议栈测试推导通过。

2026-10-19：修复“我的 IP”筛选栏样式与切换跳动。三个控件统一为 48px 高，搜索框在两类资源中共用，地区框在“全部资源”中保留位置并置灰；切换回来恢复原地区。验证与截图见 [资源筛选栏修复](docs/resource-toolbar-verification.md)。

2026-10-15：界面重做为白色主色、青蓝强调、细线分区与 4px 小圆角。登录、首页、我的 IP、设置、诊断、关于和弹窗统一样式。旧版源码和可运行包已独立保存；备份位置、恢复办法与验证说明见 [白色界面更新](docs/white-ui-redesign.md)。

Windows 10/11 x64 的 Flutter 客户端工程，目标为完整 Beta。界面、原生代码、领域包、契约、测试和脚本均在本目录内。当前实现和真实验收的差距集中记录在 [Beta 完成清单](docs/beta-completion.md)；尚未通过全部发行门槛，不能将未签名开发 ZIP 当作完整 Beta。

2026-10-13 更新：“我的 IP”使用 API 的资产名称，客户备注单独显示在详情；到期超过 72 小时的资源不再显示，恰好 72 小时和未知期限仍保留。最新客户端、生产接口实测及验证结果见 [资源显示更新](docs/resource-display-verification.md)。

## 运行与构建

在仓库根目录执行：

```powershell
# 真实 API 入口，默认连接 client-api.sunip.vip 生产服务
./Windows/scripts/run.ps1

# 显式模拟预览，无网络接管；输入任意非空账号与密码
./Windows/scripts/run.ps1 -Fixture

# Dart / Go race / 静态分析 / 契约 / UI 测试
./Windows/scripts/check.ps1

# 额外运行真实 Windows DPAPI 与桌面交互测试
./Windows/scripts/check.ps1 -NativeIntegration

# 只允许正式入口打包；当前 beta 便携包未签名
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 21

# 从生产 API 拉取账号完整分页资源列表，并逐条检查详情
python ./Windows/scripts/live-socks-check.py --account-file ./testaccount.txt

# 真实 Windows 登录、资源列表和 GUI 物理 HTTPS；不限定某个出口
./Windows/scripts/test-live.ps1 -ControlHttpIp <本次解析的生产API地址>
```

官网默认使用用户指定的 `https://sunipip.com/`，“注册账号”和“找回密码”均打开此地址；构建参数 `-WebsiteUrl` 可覆盖官网，`-Build` 指定递增构建号。默认 API 为 `https://client-api.sunip.vip/api/client/v1`。启动时读取四端共用的 `https://client-api.sunip.vip/client-config/config.json`，可远程指定下一次启动使用的 API 域名；格式、缓存和发布方法见 [共享配置](../client-config/README.md)。域名变化需要重新登录，不将旧域名令牌发送到新域名。运行脚本读取已安装工具路径，不修改系统 PATH。

产物：`dist/BlueGrass-Windows-x64-dev.zip`。从 `0.1.0-beta.1+14` 起，解压根目录只有 `BlueGrass.exe` 和 `runtime/`：直接运行外层主程序，Flutter 数据、引擎 DLL、Visual C++ x64 CRT、辅助程序和许可文件统一位于 `runtime/`；不再分发 `.lib` 等编译中间文件。每次打包成功后，脚本自动删除旧的 `dist/BlueGrass-Windows-x64/` 解压目录，再解压新 ZIP 到同一位置。已有新结构 ZIP 可用 `./Windows/scripts/expand-bundle.ps1` 重新解压。`app/build/windows/x64/runner/Release/` 仍是编译输出目录；安装器使用既有的受保护安装布局。结构和验证方法见 [便携包目录整理](docs/portable-layout.md)。模拟入口 `lib/main_fixture.dart` 拒绝 release 模式，打包脚本固定使用 `lib/main.dart`。

## 既有实现与迁移后必须保留的行为

- 密码登录、持久设备标识、DPAPI 当前用户会话存储；令牌续期串行化、持久重试 ID、迟到响应隔离。成功登录的来源 IP 和时间由服务端记录；客户端不自填 IP 或转发头。切换生产环境后提示重新登录，旧环境令牌不会发往新环境。
- 真实 API 节点分页、详情刷新、资源预选、最近成功顺序、搜索和地区筛选。“连接资源”展示 API 返回的节点，只要有效节点信息齐全即可连接，不要求预期出口 IP，不按测试出口过滤；空页游标继续加载。“全部资源”分别查询订阅和资产，明确提示当前接口的部分覆盖。
- 首页完整出口标注、资源详情、北京时间期限、设置、诊断；缺少流量数据显示 `—`，不伪造零或测速结果。
- 浅色/深色/跟随系统、系统代理/TUN 模式偏好、LAN 偏好；偏好切换不会自动连接。
- Windows 原生托盘、关闭隐藏、首次关闭提示、Explorer 恢复后重建托盘、单实例；启动时修复指向已删除旧目录的本应用快捷方式与图标关联，保留仍有效的其他安装。见 [任务栏图标修复](docs/taskbar-icon-verification.md)。原生安全存储原子替换。
- 会话失效回到登录、暂态恢复失败重试、损坏设备记录的显式清理入口、本地登出失败不谎报成功。
- Go 原生控制器：有效节点连接、请求幂等、owner/attempt 隔离、暂停/停止、到期与统计边界。建立连接后再检测出口；探测失败不关闭转发。
- 数据面迁移至内嵌 Xray-core，保留回环 HTTP 入口、经所选节点的 HTTPS 出口观测与真实流量统计。界面只显示实际结果，未取得结果显示“暂未获取”。各协议按固定能力矩阵接入，TUIC/AnyTLS 明确拒绝；构造测试与远端握手分别复验，旧内核结果不沿用。
- Flutter 与随包 `BlueGrass.Native.exe` 通过继承的私有管道通信；停止会关闭本地连接和子进程，不开放控制端口。
- 本地 HTTP 代理固定监听 `127.0.0.1:12808`，重连保持相同端口；端口被占用时不切换到随机端口，也不停止占用它的程序。首页的连接、资源选择和全局接管开关集中在同一区域，连接期间锁定模式开关。
- 当前用户 WinINET 系统代理：连接就绪即启用，随后检测出口；暂停时保留关闭的代理入口，主动断开与父进程退出时按归属恢复原设置；启动时恢复残留事务，不依赖账号或资源是否到期。
- 受保护资源切换保留 WFP 或当前系统代理设置，新资源重新校验有效性并独立检测实际出口；暂停重试复用原监听端口和逻辑会话。原生已接受的失败尝试也保留正确资源身份。
- 原生进程故障、资源到期、会话失效时保留“恢复并断开代理”入口。外部代理修改被保留，显式断开可释放旧归属记录。
- TUN 迁移按 Xray 与平台适配边界实现，FakeIP/DNS、IPv6/LAN 策略需复测；继续保留 Windows WFP 持久保护意图、已安装签名 broker/GUI/service 身份验证，以及独立修复的精确归属清理。不能把旧内核 TUN 字段或旧测试视为新核心已通过提权实机验收。
- 自启、最近成功资源、原资源自动连接、全部已知资源的独立到期摘要同步、Windows 通知与通知激活；摘要的部分覆盖明确保留，不从分页缺席推断资源已回收。
- 自动/自定义入口 DNS；自动模式有限尝试物理 DNS、1.1.1.1 和 223.5.5.5，自定义模式严格使用给定列表。服务控制流量使用经验证的物理接口绑定。
- Ed25519 更新清单、下载完整性与 Authenticode 发布者检查、续传、后台下载、实际路由对应的费用检查、用户确认安装、维护暂停和交接。发行配置仍未配置，真实签名更新尚待验收。
- 真实诊断探测库、当前资源异步诊断、脱敏导出、默认关闭的限量崩溃报告队列。没有接收端时不会上传。短信登录按用户指示保留入口，暂不作为 Beta 门槛。

## 真实连接范围

这不是已经完成 Beta 网络验收的发行版：

1. 按用户 2026-10-12 的明确修正，`expected_exit_ip` 和 `authoritative_expected_exit` 不再参与连接资格。无需测试绑定：连接后检测并显示实际出口，检测失败保留连接。行为契约见 [连接后检测出口](docs/observed-exit-flow.md)。
2. 真实联调从生产 API 获取完整节点列表并逐个连接，不限定某个资源或出口。最新结果和当前包见 [连接与出口检测验证](docs/observed-exit-verification.md)。旧 `dist/BlueGrass-Windows-x64-live-test.zip` 仅为历史产物，不再使用或构建；当前普通包不带测试绑定。
3. `winproxy/` 已通过真实 WinINET 验证。原生进程被强制结束时，下一次启动恢复自身残留设置；系统代理模式不承诺持续断线保护。LocalSystem 服务、TUN/WFP 与全局保护已有实现及隔离测试，但未完成签名提权运行、抓包、重启或多用户验收。
4. 生产服务的资源查询、资产与到期摘要接口已上线；已登录核对 `resource_queries=true`、`expiry_summaries=true`。当前仍为部分覆盖（`all_owned_resources=false`、`snapshot_complete=false`），完整归属覆盖仍待补齐。多协议参数与统一引擎已接通并通过构造与契约测试；每种协议的真实测试资源和验收分别记录。未验收 UDP、IPv6、网络切换和零泄漏，不能据 TCP 连通宣称这些能力通过。
5. Inno 安装器已有真实载荷编译验证和生产签名入口；缺少实际签名身份和已发布更新清单。`-UnsignedCandidate` 产物明确拒绝安装，只用于编译证据。旧包不会因版本名称带 beta 就成为可发行 Beta。

正式入口启动时核对本客户端残留代理及保护状态；已有 TUN 保护保持暂停，显式恢复才释放。原资源更新恢复须重新校验有效节点信息，连接后再检测出口；自启自动连接遵循保存的用户偏好。模拟入口不接管网络。直接运行 GUI 不等于安装了特权服务；TUN 需要可信安装环境。默认不上传诊断或崩溃数据。

## 源码与历史证据

下列验证链接记录各自日期、代码与产物，保留历史结论。Xray 迁移结果须新增对应证据；源码已改或包已生成均不自动更新这些报告的适用范围。

| 目录 | 内容 |
| --- | --- |
| `app/` | Flutter 界面与 Windows C++ 宿主 |
| `packages/domain/` | 资格、状态、时间、统计、提醒纯逻辑 |
| `packages/data_source/` | 客户端 API 与会话协调 |
| `native/core/` | Go 控制器、内嵌引擎、服务、WFP、更新与诊断 |
| `installer/` | Inno 安装器、信任校验、载荷检查与编译证据 |
| `contracts/` | 最小 schema 与跨语言 JSON fixtures |
| `scripts/` | 检查、打包、运行和 UI 消融 |
| `.artifacts/` | 忽略的本机验证日志与截图 |

[实现记录](docs/implementation.md) · [消融报告](docs/ablation.md) · [验证记录](docs/verification.md)

[当前连接与出口检测验证](docs/observed-exit-verification.md) · [出口流程消融](docs/observed-exit-ablation.md) · [生产 API 切换与登录 IP](docs/production-api-verification.md)（历史构建） · [此前 Beta 集成验收与发行缺口](docs/beta-final-verification.md)

[TUN 验证](docs/tun-engine-verification.md) · [WFP 与服务验证](docs/wfp-verification.md) · [更新与安装验证](docs/update-install-verification.md)

最新证据及重现命令见 [系统代理验证](docs/system-proxy-verification.md)，此前独立本地代理测试见 [历史真实连接验证](docs/live-verification.md)。测试账号文件不打包、不提交；测试会话使用独立设备 ID，完成后退出。API 默认直连，不依赖其他代理软件。

工具链锁见 `toolchain.json`。首次准备契约校验依赖可在 `contracts/` 使用 CPython 执行 `python -m pip install -r requirements-test.txt --target .schema-tools`；需要指定解释器时设置 `BLUEGRASS_PYTHON` 为该 python.exe 的完整路径，避免与 MSYS Python 混用。完整检查缺依赖或任何失败都会退出非零；未选择 `-NativeIntegration` 时会明确说明原生集成未运行。

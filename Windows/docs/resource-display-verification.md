# Windows 资产名称、客户备注与过期显示（2026-10-13）

本次 Windows 客户端接入 API 的独立 `name` / `customer_remark` 字段，并隐藏到期超过 72 小时的资源。生产 API 的名称语义由用户提供的 `20261013-102405-customer-remark` 契约定义；本轮没有修改或部署服务器程序。

## 显示规则

- “我的 IP”、首页及详情标题使用 API `name`，如 `美国-198.87.96.137`。不把备注或连接入口拼成名称，不在名称后追加编号。
- 节点、资源、到期摘要分别保留 `customerRemark`；“我的 IP”的连接资源在资产名称右侧显示非空备注，文字较小，长备注省略并可悬停查看全文。空备注不占位。详情继续独立显示“客户备注”。缺少新字段的旧 API 兼容为空备注。
- 使用 API 校准后的 UTC 时间。到期不足或恰好 72 小时仍可显示，超过 72 小时隐藏；没有到期时间的资源保留。
- 相同时间规则覆盖连接资源、全部资源、首页、打开的详情及到期提醒。现有分钟计时器更新界面时间，不新增后台查询任务。
- 隐藏是显示规则，不改变连接资格、资源身份、分页游标或原始同步记录。刚到期的节点仍不可连接；续费刷新后可重新出现。即使当前资源隐藏，已有代理的恢复／断开按钮仍可使用。

核心判断集中在 `packages/domain/lib/src/resources.dart` 的 `isResourceVisible`，各界面和提醒共用；没有引入额外资源仓库或把整个列表删掉重建。

## 自动化验证

| 验证 | 结果 |
| --- | --- |
| Domain 全量测试 | 163 通过 |
| Data source 全量测试 | 179 通过 |
| Flutter 全量测试 | 339 通过 |
| Dart / Flutter 静态分析 | 无问题 |
| 新字段回归 | 节点列表／详情、资源列表／详情、到期摘要均保留名称和备注；兼容缺失字段与空白备注 |
| 时间边界 | 72 小时及其前后、未知期限、隐藏页仍可继续分页、续费后出现、打开的详情到期后隐藏 |
| 提醒与恢复 | 已知旧通知在后续完整／部分同步中取消；显示时钟推进即可隐藏旧提醒；不禁用代理恢复 |

日志位于 `.artifacts/verification/resource-display-{domain,data}-{analyze,test}.log`、`resource-display-app-test.log`、`resource-display-app-analyze-final.log`。之前暂停时未通过的测试夹具已修正，保留了最初失败日志；备注映射也保留了实现前的失败记录 `customer-remark-adapter-red.log`。

## 生产 API 与真实 Windows 界面

本轮直接登录生产 API，账号从根目录 `testaccount.txt` 在内存读取，每次结束后退出测试会话。只记录计数和布尔验证结果，不保存账号、密码、令牌或备注内容。

实际生产响应已提供新字段：5 个节点、7 条订阅资源和 7 条到期摘要都有 `customer_remark`，各有 2 条非空备注；名称均没有旧的 `#编号` 后缀。该结论来自实际 HTTPS 响应，不把本地更新包文档中的“尚未部署”当作当前线上状态，也不声称验证了服务器二进制的构建标识。

真实桌面测试 `app/integration_test/live_resources_test.dart` 使用完整分页和真实 UI：

| 项目 | 结果 |
| --- | --- |
| API 节点 | 5 条，3 页 |
| API 订阅摘要 | 7 条，4 页 |
| API 裸资产 | 0 条，1 页 |
| “我的 IP”连接列表 | 5 个资产名称与 API 一致 |
| 客户备注详情 | 2 条均独立显示，详情标题仍为资产名称 |
| “全部资源”订阅列表 | 显示 5 条，隐藏到期超过 72 小时的 2 条 |
| 裸资产列表 | 0 条 |

证据：`resource-display-live-api-contract.json` 与 `resource-display-live-ui-verified.log`。实测不创建原生代理、不接管系统代理；测试 DPAPI 记录与会话均清理成功。本次修改没有涉及代理引擎或 TUN/WFP。

## 字段接入验证构建（build 4）

版本为 `0.1.0-beta.1+4`，Release 构建与打包校验已通过：

- 文件：`dist/BlueGrass-Windows-x64-dev.zip`，42,850,420 字节。
- SHA-256：`52aeaaa9fe02c3c291087bd58015e0f750a3eb258fde8321374219bfa1520091`。
- 32 个归档项，五个原生程序哈希与当前构建一致，CRT、Flutter 数据与许可证齐全。
- GUI、Dart AOT 和 Service 均校验了生产 API 域名；没有旧测试域名、账号文件、DPAPI 文件或测试绑定。
- 文件版本 `0.1.0-beta.1+4`，Authenticode 为 `NotSigned`；本次结束时无 BlueGrass 残留进程。

解压整个目录后运行 `BlueGrass.exe`。构建证据为 `.artifacts/verification/resource-display-release-build.log`、`resource-display-release-version.json` 和 `resource-display-bundle.json`。未提交或推送 Git；未修改 Android 或替换生产 API 二进制。原有签名发行及提权服务验收范围见 Beta 完成清单。

## 列表备注更新（build 7）

`0.1.0-beta.1+7` 在连接资源的资产名称右侧显示客户备注，保留独立的名称与备注字段。12 项相关界面测试和 Flutter 静态分析通过，覆盖名称与备注的相对位置、详情、480 像素窄窗口下的长备注和现有资源选择行为。实际打开解压后的 Release 客户端，在“我的 IP”确认了生产资源的备注与资产名称同排显示；没有备注的资源不显示占位内容。

构建脚本已删除旧解压目录并重新解压至 `dist/BlueGrass-Windows-x64/`。ZIP 共 32 项、42,851,372 字节，SHA-256 为 `dd1346bbedfa4303863319a58dd2001938be70cd44b08aa3c4032606f663b9c5`。归档检查通过，解压后的 AOT 与本次 Release 构建一致，文件版本确认为 `0.1.0-beta.1+7`。证据为 `.artifacts/verification/inline-remark-{test,analyze,release-build}.log` 和 `inline-remark-bundle.json`。

## 到期通知精简（build 9）

首页及“我的 IP”共用的通知条只显示资源数量、到期提示与查看箭头，不再展示同步说明、通知错误码和时区解释。没有待提醒资源时不显示通知条。点击仍打开到期清单，名称、到期时间和明细继续在清单中查看。

3 项提醒界面测试、Flutter 静态分析与归档校验通过；已实机确认单行通知和点击打开／关闭清单。`0.1.0-beta.1+9` 已替换 `dist/BlueGrass-Windows-x64/` 的旧解压文件，解压 AOT 与 Release 构建一致。证据为 `.artifacts/verification/compact-reminder-{test,analyze}.log`、`compact-reminder-release-build-retry.log` 和 `compact-reminder-bundle.json`。build 8 在 Dart 中间文件解析时失败，原始错误保留于 `compact-reminder-release-build.log`；使用新的构建编号重新生成中间产物后成功，未修改工具链。

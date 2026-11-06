# 生产 API、完整资源列表与登录 IP（2026-10-12）

> 后续用户已明确取消“预期出口缺失阻止连接”的规则。本页记录 build 2 当时的状态；当前连接行为以[连接后出口检测](observed-exit-flow.md)为准，最新运行与打包证据见[连接行为修正验证](observed-exit-verification.md)。本页的预期出口发行门槛已经失效。

本次按用户部署通知切换到 `https://client-api.sunip.cc/api/client/v1`。所有客户端改动留在 `Windows/`；未修改、部署服务端或直接操作生产数据库。后续用户明确要求直接拉取资源列表，因此当前验证和普通构建不再通过 `63.161.180.219` 选择某一个资源，也不带账号限定的出口绑定。

## 客户端变更

- 同步更新 Dart 默认地址及允许的 origin、C++ HTTP Host/TLS/SNI/证书主机名、Go 服务控制域名白名单、构建脚本和测试入口。旧域名仅保留在拒绝测试与历史报告中。
- 普通与 TUN 通信入口均拒绝旧 origin；旧环境的访问令牌、已过期会话及待重试刷新不会发送到生产环境。客户端提示服务地址已更新、需要重新登录。修复恢复旧环境会话失败后跳过资源定时复核的问题，重复初始化仍只创建一个计时器。
- 登录页说明：“登录成功后，服务端会记录登录时间和来源 IP，用于账号安全。”请求继续只发送用户名、密码、设备 ID 和设备名；不发送 `login_ip`、`client_ip` 或来源伪造头，也不增加 IP 探测请求。
- “连接资源”直接显示 API 返回的节点，保留搜索、地区筛选和详情；不再因为本地资格判定失败就把节点从列表隐藏。缺少固定出口的条目显示原因并禁用选择，不修改连接门禁。
- 取消测试绑定优先预选资源的特殊排序，合格资源遵循现有 API/最近成功顺序。独立“全部资源”仍按订阅和资产分页查询。
- 联调脚本默认遍历列表和详情，不要求预期 IP；`--probe-all` 对每个返回节点做诊断。诊断所得出口不写回为权威预期出口。API 请求拒绝全部重定向。

## 生产实测

生产域名本次解析到 `192.140.166.247`；这是显式 HTTPS smoke 的临时输入，不写入产品配置。直连健康接口返回 200，C++ 物理接口绑定传输以生产域名完成 SNI/证书验证并返回 200。

用户更新根目录 `testaccount.txt` 后，生产登录和登出通过。凭据仅由本机文件读取；日志和产物不包含密码、节点凭据或令牌。

使用完整分页查询，当前账号返回：

| API | 当前结果 |
| --- | --- |
| `/nodes` 与每条详情 | 4 个节点，全部读取，无出口筛选 |
| `/resources?kind=subscription` | 6 条订阅摘要，分页结束 |
| `/resources?kind=asset` | 0 条资产摘要，分页结束 |
| `/capabilities` | 资源查询与到期摘要为 true；全部权威资源覆盖、权威出口与短信登录为 false |
| 节点出口字段 | 4 个 `expected_exit_ip` 均无值 |

4 个节点均已独立完成 SOCKS HTTPS 诊断，使用第三方 `api.ipify.org`，没有要求其出口等于同一个 IP，也未创建绑定。日志：`.artifacts/verification/production-all-resources.log`。分页结束不等于权威完整快照；生产接口仍明确 `snapshot_complete=false`。

登录 IP 属于服务端会话写入逻辑，客户端不新增存储或历史端点。本次验证了真实生产登录与本地服务端 13 种来源识别/可信代理契约场景；没有直接查询生产会话表，因此不将客户端登录成功表述为已独立核验生产数据库中的具体 `login_ip` 值。

## 回归与消融

| 检查 | 结果 |
| --- | --- |
| 领域规则 | 129 项通过 |
| API 与会话 | 166 项通过；静态分析通过 |
| Flutter、IPC、界面 | 最终 325 项通过；`flutter analyze` 无问题 |
| Go 原生 | 全包 `with_gvisor`、race、vet 通过 |
| 共享契约 | 12 项通过 |
| 服务端登录 IP 契约 | 本地 13 个来源/代理场景通过，未访问生产数据库 |
| 联调脚本重定向 | 20 种离线子场景通过 |
| 真实 Windows 桌面 | 普通直连和 GUI 物理绑定两种路径均登录成功，4 个节点全部可见；另一次物理绑定健康读取通过 |
| 本轮消融 | 2 项，均基线通过、删除后出现行为失败、恢复后通过；共享源文件哈希未变 |

原生 C++ 编译、协议/证书测试及生产健康读取通过。统一检查日志为 `.artifacts/verification/production-api-unified.log`；后续资源展示与重新登录修复的最终界面回归另存 `production-app-test-final.log`、`production-app-analyze.log`。旧 origin 测试保留改动前的失败日志与改动后的通过日志。

桌面测试主动使用每页 2 条：节点遍历 2 页，订阅摘要遍历 3 页，空资产遍历 1 页；窗口确认 4 个节点均可见且没有测试绑定。两种传输的日志为 `production-live-desktop.log` 和 `production-live-resources-direct.log`。首轮在能力接口阶段失败，日志保留为 `production-live-desktop-first-attempt.log`；增加安全错误分类后复测通过，未取得首轮失败的具体根因。

测试后 `10808` 监听地址及 PID 与开始时一致，WinINET 代理设置未变，没有残留 BlueGrass 进程；见 `production-api-host-before.json` 和 `production-api-host-after.json`。

API 包新增真实回环 HTTP 登录请求检查，以及三种旧环境会话状态的隔离测试。联调脚本重定向测试覆盖 GET/POST、301/302/303/307/308、跨域 HTTPS 和 HTTPS 降级 HTTP，全部在本地假传输中运行，不访问账号或网络。

本次消融记录见[生产 API 消融](production-api-ablation.md)。早期 25 项行为消融见[原有报告](ablation.md)。保留必要的 origin、会话环境和 TLS 校验；删除列表资格过滤和测试绑定预选优先级，没有新增客户端 IP 发现、上报、存储或历史抽象。

## 复现

```powershell
./Windows/scripts/check.ps1
python ./Windows/scripts/live-socks-check.py --account-file ./testaccount.txt --probe-all
./Windows/scripts/test-live.ps1 -ControlHttpIp 192.140.166.247
./Windows/scripts/ablate-production-api.ps1
./Windows/scripts/build.ps1 -Version 0.1.0-beta.1 -Build 2
python ./Windows/scripts/verify-release-bundles.py --production-only
```

重跑前先解析生产域名，更新 `ControlHttpIp`。本次默认桌面联调验证完整列表和健康读取，不启用系统代理或 TUN。旧 `live-test.zip` 与 build 1 安装器是切换前的历史产物；本次普通包不包含测试绑定。

## 当前构建产物

普通 Release 使用 `lib/main.dart`，版本 `0.1.0-beta.1`、build 2。ZIP 为 `dist/BlueGrass-Windows-x64-dev.zip`，大小 **42,852,078 字节**，SHA-256：

```text
184c9beee615142c9a949dfc5b5b7b37a8d36c4868b83fff8bf5987b0c2026d4
```

`scripts/verify-release-bundles.py --production-only` 已核验 32 项 ZIP 清单、五个原生程序哈希、必需 DLL/CRT/许可、没有账号/会话/测试绑定；GUI EXE、Dart AOT 和服务二进制均含生产域名，不含旧域名。证据为 `.artifacts/verification/production-api-bundle.json`。

解压整个目录后运行 `BlueGrass.exe`。未压缩副本在 `app/build/windows/x64/runner/Release/`，同样不带测试绑定。该包仍未签名。

安装器的真实载荷快照位于 `.artifacts/installers/0.1.0-beta.1-2-5ebfc24bd52f43a498d2c14602614339/`。30 个载荷文件哈希、原生构建来源和运行权限清单校验通过；Setup SHA-256 为 `695b153dab7da9e18df2628c3c8e478cdc25e623c55981352f9446ac605fb8f0`。它仍是 **UnsignedCandidate 编译证据，禁止安装**，不是可发行安装包；本次没有执行安装器或修复程序。

## 发行边界

生产资源接口的部署门槛已解除。仍需 API 的权威固定出口数据，才能让普通客户端通过连接前的固定出口校验；缺失字段不会再让资源从列表消失。签名身份、真实更新发布配置和签名提权 TUN/WFP/安装生命周期验收等剩余门槛仍见[此前 Beta 验收记录](beta-final-verification.md)。未签名 ZIP 不代表完整 Beta 已通过发行验收。短信登录按用户要求继续预留。

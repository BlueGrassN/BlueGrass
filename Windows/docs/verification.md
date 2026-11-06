# Windows 验证记录

2026-10-12 的最新系统代理接管、恢复及实机结果见 [系统代理验证](system-proxy-verification.md)。下文为首轮历史基线，包大小、测试数量和能力边界不代表最新构建。

本文件记录首轮界面与控制语义基线。后续 sing-box、私有进程管道和指定资源的真实连接结果见 [真实连接验证](live-verification.md)；下方“未取得”的历史状态已由该报告按实际证据更新。

日期：2026-10-11（Asia/Hong_Kong）。环境见 `../toolchain.json`，全部命令在本机 Windows 执行。

## 已执行证据

| 检查 | 结果 |
| --- | --- |
| Dart 领域规则 | 73 项通过 |
| API 适配与会话 | 68 项通过 |
| Flutter 控制器/存储适配/界面 | 审阅修复后 23 项通过 |
| Go race | 审阅修复后 41 个顶层测试，139 个含子测试通过；0 失败/skip |
| Go vet | 通过 |
| JSON Schema / 共享向量 | 8 项通过 |
| Windows 原生集成 | 2 项：DPAPI 与桌面模拟流程通过；最终统一检查已再次通过 |
| Debug / Release 编译 | 审阅修复后重新编译成功，已核对最终 Release 程序实际登录窗口 |
| API 健康接口 | 2026-10-11 20:49 HKT，`success:true`、`data.status:ok`；未登录 |
| 消融实验 | 13 个删减安全机制的变体触发原回归；恢复后通过；3 项等价简化已保留 |

主命令为 `./Windows/scripts/check.ps1 -NativeIntegration` 与 `./Windows/scripts/build.ps1`。
统一日志在 `../.artifacts/verification/final-check.log`、`release-build.log`，每个模块另有完整日志。
审阅修复后的统一检查已退出 0，所有模块分析和测试通过；该次 Go race 使用 `-count=1`，未把缓存结果作为执行证据。

## Windows 集成具体验证

使用真实 C++ MethodChannel：

- DPAPI 记录加密落盘，磁盘字节不含测试明文；重建存储对象后可以读取。
- 完整记录替换不保留旧字段，删除后读取 null。
- `../escape` 非法键被拒绝，测试只创建随机命名的自身记录并在 finally 清理。
- 原生 Windows 窗口中执行模拟登录、预选但不连接、模拟连接/断开、主题切换不丢失登录状态、登出。

视觉检查：

- [登录](../.artifacts/screenshots/login.png)
- [浅色首页](../.artifacts/screenshots/home-light.png)
- [深色设置](../.artifacts/screenshots/settings-dark.png)

截图来自 Windows 集成测试的实际渲染，均明确展示模拟环境。宽/窄窗口回归检查五个主页面，无 Flutter 异常或溢出。

## 独立审阅

独立只读审阅发现会话失效 UI、损坏记录恢复、暂态恢复入口、偏好失败回滚、原生 null 解码及实际模式六项问题；另行发现本地登出误报。已分别核对并增加复现回归。RED/GREEN 原始输出见 `review-ui-red.log`、`review-ui-green.log` 与原生 [修复报告](../native/core/verification.md)。

前两个恢复问题中，恢复方法本身可重试，缺的是用户可操作的入口；已用 Widget 测试实际点击重试和打开/取消修复确认框验证，没有把已有的通过测试说成失败复现。

## 未取得的运行证据

最终开发包：`../dist/BlueGrass-Windows-x64-dev.zip`，12,892,387 字节。
SHA-256：`E819E36C506F7A716FC300DBCB42BA910A615F4A7B8552988A017ECC52832325`。
包内已核对 BlueGrass.exe、Flutter 引擎/数据与 Visual C++ x64 CRT；程序元数据为 BlueGrass / 0.1.0+1，启动后正常显示真实 API 登录页，未进行登录。最终程序保持打开供本机查看。

没有测试账号配置，不含真实登录与付费线路测试。当前代码没有真实 Windows 网络服务、sing-box、可信 IPC、WFP/TUN 或代理恢复实现，不能据以上测试宣称零泄漏、UDP/IPv6 可用、开机保护有效或 Beta 就绪。未测试 Windows 10 实机、多用户/系统关机、Explorer 崩溃恢复和恶意进程竞争。发布签名、升级及安装卸载也未验收。

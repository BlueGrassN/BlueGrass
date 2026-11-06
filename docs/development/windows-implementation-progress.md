# Windows 开发记录（2026-10-11）

> 历史记录说明（2026-10-19）：本文保留原日期的方案、缺口和测试/构建事实；其中 sing-box 主内核、旧协议全量承诺及 libbox 路线已由 [ADR 0049](../adr/0049-xray-only-four-platforms.md) 替代。四端仅使用 Xray-core，旧报告不能作为新核心或其他平台的验收证据。原固定预期出口门槛已撤销，短信为可选能力。

依据：`docs/superpowers/plans/2026-10-11-bluegrass-development-kickoff.md`，D-01～D-19、D-23～D-48。

用户本轮明确授权开始 Windows 客户端开发。当前实现目标是可构建、可运行的 Windows 内部开发版本，连接生产放行仍受已确认门禁约束。

## 执行决定

- 在 `feature/windows-client` 分支、当前工作目录开发，以保留尚未提交的 D-23～D-48 及启动计划；不复制过时 HEAD 到新 worktree，不提交或推送原有修改。
- 本轮按 Windows 范围实现 K-01 的工具与检查入口、K-02 的所需共享契约、K-05/06/07/08。Android 验证、既有 API 后端修复 K-03/04 和真实平台 Spike 独立登记，不改变服务端或生产数据。
- 真实 API 当前缺 expected_exit_ip；适配器可登录/分页/刷新，但连接配置缺固定出口时必须拒绝。开发连接使用显式 fixture 构建，不能作为网络接管证据。
- 按用户最新目录要求，Flutter 工程迁至 `Windows/app/`，领域与数据源包位于 `Windows/packages/`，Go 控制器位于 `Windows/native/core/`，契约和脚本也全部位于 `Windows/`。不生成 Android 工程。
- 预检接口：K-02→K-05 使用命名空间资源键、UTC/null、精确计数；K-06→K-08 使用统一会话与数据源；K-07→K-08 当前只建立可替换边界，无可信 IPC 时实际客户端不得报告连接成功。

## 工作状态

- [x] 工具链现场核对：Windows 编译条件具备；本进程 PATH 需读取已安装工具路径。
- [x] 最小共享契约与 Dart 领域逻辑（完整 K-02 契约仍未完成）。
- [x] API 会话、续期及分页适配。
- [x] Go 原生控制参考实现及 race 测试。
- [x] Windows Flutter 界面、安全存储、偏好与模拟流程。
- [x] 首轮静态分析、单元/交互测试、Windows 编译；后续修复后复验见下方记录。
- [x] 独立审阅与已知限制记录，审阅发现进入修复回归。

详细状态、消融和最终验证以 [Windows 开发说明](../../Windows/README.md)、[实现记录](../../Windows/docs/implementation.md)及[验证记录](../../Windows/docs/verification.md)为准。

## 不能预先声称完成的范围

真实 sing-box、Windows 服务/安全 IPC、系统代理归属恢复、TUN/WFP、真实出口服务、短信、全量资产及提醒、签名更新与正式安装包，均需各自实现及验收证据。
> 更新于 2026-10-12：本文为早期进度记录。旧的固定预期出口门槛和测试绑定已撤销；当前 Windows 客户端连接有效节点后检测实际出口，失败不阻断连接。详见 [当前连接流程](../../Windows/docs/observed-exit-flow.md) 与 [最新验证](../../Windows/docs/observed-exit-verification.md)。


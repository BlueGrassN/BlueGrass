# K07 验证与消融记录

日期：2026-10-11。目录：`core`。
工具链：Go 1.27.2 windows/amd64；race 使用 `C:\msys64\ucrt64\bin` 的 GCC，CGO_ENABLED=1。

P2 修复后最终 `go test -race -json -count=1 ./...` 退出 0：41 个顶层测试，139 个测试/子测试通过，
失败事件 0、skip 0。`go vet ./...` 退出 0。gofmt 无待格式化文件。

## 独立审阅 P2 修复

本轮只修改 `control/decode.go`、`control/controller.go`，新增
`control/review_regression_test.go` 并更新本报告。未执行消融或临时突变，未修改范围外文件。

先运行新增六项回归，RED 实际退出 1：

- `TestNullUnavailableCannotRemoveDeadline` 复现
  `null unavailable accepted: disposition=applied applyError=<nil> deadlineRemoved=true`。
- `TestWireRejectsNullNonnullableFields` 复现 sequence、schemaVersion、身份、profile、
  认证及更新字段的 null 被当作零值，包含 Hello/GetStatus 标量。
- `TestUpdateRequiresJSONBooleanAndAllowsNullDeadline/null` 复现非布尔 null 被接受。
- `TestPrepareFailureDoesNotClaimRequestedMode` 复现
  `unprepared mode reported: ack=tun failed=tun`。
- `TestActualModePublishedOnlyAfterPrepareSuccess` 复现准备尚未完成就报告请求模式。
- `TestLatePrepareCannotPublishModeForNewAttempt` 复现新 attempt 未完成 Prepare 就报告模式。

修复：在严格对象校验中拒绝非期限字段的 JSON null，再由类型解码要求 unavailable
为布尔、sequence/schemaVersion 等为对应类型。仅 `validUntilUtc` 保留 null 语义；
明确的 true/false 及未绑定握手省略可选会话字段均有正向测试。
Connect 将 ActualMode 标为 unknown，只有当前 attempt 的 Prepare 成功后才发布模式；
失败和迟到的旧 Prepare 都不能宣称请求模式已经生效。

两组定向回归分别转绿后，重新运行完整 race/vet：

```text
go test -race -json -count=1 ./...
exit=0; top-level passed=41; all test/subtest passes=139; failure events=0; skipped tests=0
go vet ./...
exit=0
```

本轮结果直接从当前命令 JSON 事件计数，未重写范围外旧日志；下方原始日志和消融
记录属于先前 35 顶层/79 项基线，不是本轮 41/139 的原始测试流。

## 初始实现与历史消融

K07 指定八项均执行：TestGateClosedUntilMatchingIPv4、TestLateProbeCannotReleaseNewAttempt、
TestExpiryPausesWithoutRelease、TestStopRequiresCurrentOwner、TestDuplicateRequestIsIdempotent、
TestSameRequestDifferentPayloadRejected、TestUpdateExpiryCannotReviveCancelledAttempt、
TestUntaggedCounterIsIgnored。

TDD：先有可编译的无行为控制器骨架，首轮行为测试实际失败；实现后通过。
后续测试先复现非法时钟仍开门、逻辑会话混用资源、缺字段、公共端口/计数边界和
迟到续期绕过旧期限，再修复并跑完整套件。

真实消融（临时修改生产代码，运行已有测试，finally 恢复）：

| 临时删除 | 原测试实际结果 |
| --- | --- |
| gate 中 expected == observed | TestGateClosedUntilMatchingIPv4/mismatch 失败：want failed got connected；退出 1 |
| currentLocked 中 attempt 隔离 | TestLateProbeCannotReleaseNewAttempt 失败：old probe released traffic；退出 1 |
| 恢复上述两项 | 完整 go test ./... -count=1 退出 0 |

实际删除的冗余设计：attempt 中重复的完整 ControlContext，以及 currentLocked 内重复的
closed/snapshot/context 比较。每次 Connect 分配唯一 attempt，所有取消/关闭/owner 切换
都会先取消并清空 active；保留 `active == attempt && ctx.Err() == nil` 足以验证当前身份。
删除后完整测试通过，再执行最终 race/vet，未保留临时消融代码。

初始实现的历史原始日志位于忽略的构建产物目录：

- [race JSON 事件](../Windows/.artifacts/verification/native-test-evidence.jsonl)
- [vet 结果](../Windows/.artifacts/verification/native-vet-evidence.txt)
- [真实消融和等价回归](../Windows/.artifacts/verification/native-ablation-evidence.txt)

文件清单：go.mod；engine/engine.go；clock/clock.go；gate/gate.go；
control/types.go、controller.go、snapshot.go、decode.go；
control/controller_test.go、boundaries_test.go、shared_fixtures_test.go、review_regression_test.go；README.md、本报告。

未验证/未实现：真实 Windows engine/service adapter、可信 IPC/归属鉴权、
WFP/TUN/系统代理、sing-box、真实探测与流量计数、休眠/重启持久化及平台零泄漏。
共享向量确实读取同一文件，但 NodeRecord 资格、Dart 过渡状态、UI 速度并非原生等价输出；
具体映射及剩余 K-02 契约缺口见 README。无可信服务时生产必须 fail closed。

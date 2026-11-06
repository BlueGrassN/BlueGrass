# 当前用户 Windows 系统代理事务

本包供主代理的 Go engine/IPC 直接调用；只负责当前交互用户的 WinInet 默认 LAN
设置，不负责监听端口、启动/停止代理、网络探测、WinHTTP、RAS、TUN 或 WFP。
不得从 LocalSystem 服务直接调用并将其视为登录用户配置；宿主应在该用户的进程中调用。

## 接口（先确定的集成契约）

```go
type Settings struct {
    Flags uint32
    Server, Bypass, AutoConfigURL string
}
func New(statePath string) (*Manager, error)
func NewWithDependencies(backend Backend, store Store) (*Manager, error)
func (*Manager) Apply(server string, bypass string) error
func (*Manager) Restore() error
func (*Manager) Recover() error
func (*Manager) Relinquish() error
func (*Manager) State() (bool, error)

type Backend interface {
    Query() (Settings, error)
    Set(Settings) error
    Notify() error
}
type Store interface {
    Load() (*Record, error) // 没有记录返回 nil, nil；其他错误必须返回
    Save(Record) error     // 返回 nil 前必须完成持久化和原子替换
    Delete() error         // 不存在视为成功
}
type Record struct {
    Version int
    Original, Applied Settings
    Phase string
}
```

`New` 不读写系统代理，也不自动恢复。Windows 使用 WinInet + current-user DPAPI；
非 Windows 返回 `ErrUnsupported`。注入构造在所有平台可用。不要复制 Manager；
一个用户只能有一个管理进程/Manager，所有调用在 Manager 内串行。跨进程单实例和
固定、受该用户 ACL 保护的本地 statePath 由宿主保证；本包不提供跨进程锁。

`Apply` 目标为 Flags=`ProxyTypeDirect|ProxyTypeProxy`，保存传入的 Server/Bypass，
清空 AutoConfigURL 并关闭 PAC/自动检测。Server 必须非空，字符串须为有效 UTF-8 且不得含 NUL。
原始四字段（包括 PAC URL 和全部 flags）原样备份。备份准备完成后再次检查当前值，
再 Set、Notify、Query 逐字段精确验证；验证通过后才保存 applied 阶段。
已应用相同目标时幂等；不同目标返回 `ErrRecoveryRequired`，应先 Restore 再 Apply。

记录包含 Version=1、Original、Applied、Phase（prepared/applied/restoring）。
错误可使用 `errors.Is` 判断：`ErrOwnershipLost` 的错误文本含 `OWNERSHIP_LOST`；
另有 `ErrRecoveryRequired`、`ErrVerificationFailed`、`ErrInvalidSettings`、
`ErrInvalidRecord` 和 `ErrUnsupported`。错误中不输出代理/PAC 字符串。

`Restore` 只有当前四字段完全等于 Applied 时才写回 Original。先持久化 restoring
阶段，再次查验归属，然后写回、通知、验证，最后删除记录。第三方修改任何字段均
返回 `OWNERSHIP_LOST`，不写设置、不删除记录。恢复已经写入 Original 但通知/删除
失败时，可重试通知和验证后删除记录，不再次写代理。失败始终返回错误并保留记录。
`Recover` 等价于 Restore，用于宿主明确触发的启动恢复，不自动接管任何代理。
prepared 阶段若当前值仍为 Original，通知并核对后清除未写入的事务，不重复写设置；
其他未匹配值仍拒绝恢复。Apply 失败不会自行尝试回滚。

`Relinquish` 是显式释放过时归属：只有当前设置已不同于 Applied 才能删除旧记录，
不写代理且不声称恢复了 Original。宿主在主动 Disconnect/ReleaseProtection 时使用它，
并以 `proxySettingsPreserved` 告诉界面当前外部设置被保留。启动发现归属丢失仍提供 Hello 和恢复入口；
EOF 清理不会擅自丢弃归属丢失记录。`nativehost/system_windows.go` 使用每用户进程锁保护整个生命周期。

`State` 是只读检查：没有记录返回 false,nil；applied 且设置完全匹配返回 true,nil；
不匹配返回 false,ErrOwnershipLost；待恢复阶段返回 false,ErrRecoveryRequired。
bool 不表示网络连通、engine 存活、通知成功或零泄漏。调用者必须处理 error。

原始配置包含敏感信息，记录不应输出到日志。磁盘只保存 DPAPI 密文，使用同目录
临时文件写入、Sync、关闭，再通过 MoveFileExW 的 REPLACE_EXISTING|WRITE_THROUGH
替换。加解密禁止 UI，不设置 LOCAL_MACHINE。记录是本包生成的版本化规范 JSON，
拒绝缺字段/重复字段/未知字段/损坏/超限数据，不尝试修复后恢复；单个记录上限 1 MiB。
本包不把 Windows 上的 Go 文件 mode 当作 ACL 保障，宿主必须保护 statePath 目录。

WinInet 没有 compare-and-swap；查询与写入之间外部程序仍可能竞争。同值第三方写入
也无法辨别。本包在写入前重查并在写入后验证，但不声称可以消除这些系统 API 限制。

## 实现和测试计划

1. 先用注入 Backend/Store 写失败测试，覆盖完整快照、持久化顺序、归属丢失、崩溃恢复、
   查询/写入/通知/验证/删除失败和幂等行为，再实现 manager.go。
2. 先测加密失败/原子替换失败/损坏记录，再实现 store.go 与平台文件操作；测试使用
   fake 加解密和临时目录，不调用真实 DPAPI。
3. 编写 Windows syscall 封装和非 Windows stub；Windows API 边界测试用 fake 调用，
   检查 ABI、四字段、flags、通知及缓冲区释放，不调用任何真实 WinInet 操作。
4. 跑本包回归、race、vet 和跨平台编译；只修改本目录，不修改 go.mod。

## 安全验证命令

从 `core` 运行：

```powershell
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly -count=1 ./winproxy
$env:PATH = 'C:\msys64\ucrt64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly -race -count=1 ./winproxy
& 'D:\Program Files\Go\bin\go.exe' vet ./winproxy
```

32 位回归（同样只跑 fake），以及仅编译 Windows arm64/Linux stub：

```powershell
$env:CGO_ENABLED='0'
$env:GOOS='windows'; $env:GOARCH='386'
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly -count=1 ./winproxy
$env:GOARCH='arm64'
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly -c -o ./winproxy/winproxy-arm64.test.exe ./winproxy
$env:GOOS='linux'; $env:GOARCH='amd64'
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly -c -o ./winproxy/winproxy-linux.test ./winproxy
Remove-Item -LiteralPath './winproxy/winproxy-arm64.test.exe','./winproxy/winproxy-linux.test'
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED
```

所有单元测试必须显式注入 fake，不使用 New 创建生产 Manager，不做真实系统接管。
不运行 WinInet 真实写入；不读取 testaccount.txt；不操作 127.0.0.1:10808、已有代理
服务或宿主网络。主代理未来只能在用户明确请求下用独立 ephemeral 端口联调。

API 依据：[WinInet per-connection options](https://learn.microsoft.com/en-us/windows/win32/api/wininet/ns-wininet-internet_per_conn_optionw)、
[连接设置与作用域](https://learn.microsoft.com/en-us/windows/win32/wininet/setting-and-retrieving-internet-options)、
[DPAPI](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata)。

## 本次验证记录（2026-10-11）

Go 1.27.2，Windows amd64；只运行本包，不运行其他代理/engine 集成测试。

| 检查 | 结果 |
| --- | --- |
| 事务、文件存储、WinInet、DPAPI/原子替换的首轮测试 | 先观察到未实现导致的断言失败，再实现并通过 |
| Windows amd64 `test -mod=readonly -count=1 ./winproxy` | 通过，28 个顶层测试，另有故障子用例 |
| Windows amd64 `test -mod=readonly -race -count=1 ./winproxy` | 通过 |
| `go vet ./winproxy` | 通过 |
| Windows 386 fake 单元回归 | 通过（含 ABI 布局） |
| Windows arm64 / Linux amd64 `test -c` | 编译通过，未运行 |
| 实际 WinInet、DPAPI、系统代理接管、网络连通 | 未运行，不能据此宣称真实接管验证通过 |

最终为作者自审（没有可用的独立子代理审查工具），核对了写入前持久化、全部字段
比较、每个错误路径保留记录、通知失败重试、临时文件清理与本目录修改边界。
宿主必须落实当前用户进程、单实例、稳定且受保护的备份路径；WinInet 无 CAS 的
竞争窗口与 prepared 无归属时保守拒绝恢复的行为按前述接口保留。

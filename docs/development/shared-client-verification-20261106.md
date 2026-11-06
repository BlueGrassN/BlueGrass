# 共享客户端迁移验证（2026-11-06）

迁移基线为 `6328f6cc51252561b4dbe56e8982251584782342`，共享实现提交为 `381289dfca02cadb2230d6c2263791f937c3e56c`。架构与开发入口见[共享客户端架构](shared-client-architecture.md)。

## 本地验证

- domain 164、data_source 179、共享 client 34、Windows 440、Android 168、Mac 宿主 3 个测试通过；共享包和宿主静态分析通过。
- 根 Go 核心完整测试、Windows 打包中的 race/vet、Android 桥接与实际 ARM64 Xray 原生库构建通过。
- 契约 13、统一发布器 15、Mac 打包工具 11 个测试通过；Actionlint 通过（沿用 `macos-15-intel` runner 名称表例外）。54 个 PowerShell、13 个 Python 工具语法检查通过。
- 原生、系统代理、生产 API、出口观测以及控制器 3 类消融均验证了正常基线、实际行为失败与精确恢复。
- 窄屏 320dp / 1.8 倍字体的出口 IP 布局回归通过，覆盖亮暗主题、文本选择与桌面布局。
- 16 个固定路由资源与迁移前 Git blob 字节一致；品牌资源与上游哈希未改写。
- Windows 本地 `package.ps1 -Version 0.1.0-beta.1 -Build 31` 成功，含 ZIP、Wintun、helper 启动/退出与端口释放检查。
- Android 本地 `0.5.0+9` ARM64 APK 构建成功：20,583,269 字节，SHA256 `651f215ce20486cbe8522da2274c26fc75551338fbf0d9186b999784b2c16de5`。这是本地开发签名包，不是线上发行包。

## 云端与测试竞态

共享实现提交的 [Windows 运行](https://github.com/BlueGrassN/BlueGrass/actions/runs/42182418687)、[Android 运行](https://github.com/BlueGrassN/BlueGrass/actions/runs/42182423158) 成功。[首次 Mac 运行](https://github.com/BlueGrassN/BlueGrass/actions/runs/42182427240) 中 x86_64 成功，arm64 在原有 `TestExpiryTimerRunsWithoutUI` 失败。

该测试只等待 `paused`，但定时器在获取引擎锁、关闭业务通道前就会发布这个状态。本地锁住引擎锁可稳定复现 `expired=true, gateOpen=true, intent=true` 的中间快照。修正仅让测试在原有 2 秒上限内等待完整到期结果，并检查最后的引擎调用为 `close, stop`；保留 80ms 真实定时器，不修改生产代码或直接调用到期方法。

修正后到期定时器测试连续 100 次通过，`go test -race ./control` 通过。[补充 Mac 运行](https://github.com/BlueGrassN/BlueGrass/actions/runs/42185454795) 的 arm64 与 x86_64 均成功，覆盖共享/原生测试、隔离 utun/PF 检查、开发包构建、提权启动与平台契约。

## 主分支来源

共享源码已快进到 main：`1583186851dc327dc39e1842bb8eee459c2b05c2`。对应构建为 [Windows](https://github.com/BlueGrassN/BlueGrass/actions/runs/42186579789)、[Android](https://github.com/BlueGrassN/BlueGrass/actions/runs/42186579818)、[macOS 双架构](https://github.com/BlueGrassN/BlueGrass/actions/runs/42186579582)。后续发布器和文档修订不改变这些安装包的实际源码来源。

三端主分支构建均成功。发布核验发现 GitHub 的 `status=success` 列表漏掉已成功的 Mac 运行，普通分支列表则已返回它；统一发布器改为读取分支列表后自行执行原有严格状态与来源筛选。新增回归先验证旧实现仍保留旧包的失败，再验证新实现更新合格包且拒绝更晚的 PR、fork、失败、运行中与非 main 记录；发布器共 16 项测试及 Actionlint 通过。

## 本地开发目录交接

原开发目录在同步前核对了 1,325 个基线文件哈希，无其他任务写入；通过 Git 快进迁移源码，保留原有 `.gitattributes` 注释修改。旧目录仅剩的生成文件与日志归档到本地忽略的 `.artifacts/shared-client-handoff-20261106/legacy-generated`，没有递归删除构建缓存或上传测试日志。

三个共享包和 Windows、Android、Mac 宿主均重新解析依赖，Mac 资源同步完成。契约测试依赖随目录迁移，原目录运行 `contracts/test_contracts.py` 的 13 项检查通过。现有 Windows 原生输出目录 `Windows/native/bin` 保持可用。

## 验证边界

自动测试、构建与 macOS runner 的隔离 utun/PF 检查不等于用户设备上的真实代理联网、睡眠唤醒、权限恢复或长期运行验收。Android 非 main 构建使用测试签名；线上签名以 main 运行的实际元数据为准。Mac 开发包仍不代表 Apple Developer 正式签名或公证。各端最终来源与哈希以固定 [preview 页面](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview) 的逐端记录为准。

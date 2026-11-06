# Windows 本地构建与 GitHub Release

## 目录与入口

仓库按平台组织：`Windows/`、`Android/`、`Mac/`、`iOS/`、`HarmonyOS/`；`client-api/` 为服务端，`client-config/` 为远程客户端配置，`assets/` 为公共素材，`docs/` 为共享文档。GitHub 工作流位于根目录 `.github/workflows/`。

Windows 主入口是 `Windows/scripts/package.ps1`。本地与 GitHub 都运行这个脚本，依次执行业务包/Flutter 分析及测试、Go 竞态测试与 vet、契约检查、原生与 Flutter 构建、解压文件校验、便携助手测试。真实账号联网、TUN/WFP 接管及签名安装不属于 CI 检查。

`.gitattributes` 对已锁定哈希的路由许可证和 Wintun 许可证禁用换行转换；不能用重新计算哈希的方式掩盖 checkout 改字节的问题。打包清单同时覆盖当前 Loyalsoldier 数据的上游许可证。

## 本地生成测试客户端

安装 `Windows/toolchain.json` 指定的 Flutter、Go，以及 Visual Studio 2022 C++ 桌面开发工具和 Windows SDK。PATH 需能找到 `gcc`、`windres`；本机原有 MSYS2 UCRT64 路径可作为后备。CPython 不可使用 MSYS Python。

在仓库根目录执行：

```powershell
$env:BLUEGRASS_PYTHON = '你的 CPython python.exe 完整路径'
& $env:BLUEGRASS_PYTHON -m pip install -r contracts/requirements-test.txt
./Windows/scripts/package.ps1
```

公开版本读取根 `release.json`，内部构建号读取 `Windows/release.json` 的 `buildBase`。显式版本必须等于公共配置，例如 `./Windows/scripts/package.ps1 -Version 0.1.0 -Build 22`。开发时可使用 build.ps1，CI 必须运行完整 package.ps1。

产物：

- `Windows/dist/BlueGrass-Windows-x64/BlueGrass.exe`：已解压的本地客户端，旁边仅有 `runtime/`。
- `Windows/dist/BlueGrass-Windows-x64-dev.zip`：原有本地便携包路径。
- `Windows/dist/release/BlueGrass-Windows-x64.zip`：供 Release 上传的相同包。
- `Windows/dist/release/SHA256SUMS.txt`：ZIP 和构建信息文件的 SHA256。
- `Windows/dist/release/build-info.json`：版本、构建号、来源提交、工作区是否有未提交修改、云端运行编号和 ZIP 哈希。

每次成功构建会先删除旧的固定解压目录，再解压新包。若旧目录中有客户端运行，脚本会在删除任何文件前报错；先正常退出旧客户端再运行，避免锁文件造成半新半旧。脚本不会自动终止现有客户端，也不会改动 10808 代理服务。

## GitHub 自动构建和发布

工作流为 `.github/workflows/windows-release.yml`，使用 `windows-2022` 构建，工具版本从 `Windows/toolchain.json` 读取；Action 固定到具体提交。

| 触发方式 | 行为 |
| --- | --- |
| Windows 相关路径推送到 main | 检查、构建、保存 artifact，成功后发布预发布 Release |
| 涉及相同路径的 PR | 检查、构建、保存 artifact，不发布 Release |
| Actions 页面手动运行，选择 main | 同 main 推送 |
| 手动选择其他分支 | 仅构建 artifact |

触发路径包含根 `release.json`、Windows、packages、core、contracts、Windows 工作流及根 Git 属性/忽略规则。公共版本变更必须触发三端构建。

云端构建号为 `buildBase + github.run_number`，客户端版本和构建号保持独立；构建号不能超过 65535，也不能重置工作流编号后倒退。

成功构建由 publish-preview.yml 汇总到固定 preview；三端公开版本统一读取根配置。全部平台（Mac 含双架构）成功并验证来源、内部构建号和哈希后，先上传完整新附件，再整体更新页面，最后清理上一版本附件。任一平台缺失或验证失败均保留上一整组下载。Windows sourceDirty 标记如实保留。

构建 job 保持 `contents: read`；统一发布 job 才拥有 `contents: write`，使用 Actions 自动提供的令牌，不需要个人令牌或测试账号。并发、重跑及中断恢复见 [统一发布手册](../../docs/development/github-releases.md)。

下载入口：[统一开发预览版](https://github.com/BlueGrassN/BlueGrass/releases/tag/preview)。公开 ZIP 示例为 `BlueGrass-Windows-0.1.0-x64.zip`，校验文件和构建信息使用平台/X.X.X 前缀，构建号仅保留在元数据。同版本文件不可覆盖，下一次发行先递增根版本。旧平台 Release 已清理，私有仓库下载仍需访问权限。

## 下载和检查

下载 Release 中的 ZIP 并完整解压，运行最外层 `BlueGrass.exe`，保留同级 `runtime/`。不要只复制 EXE。可用 `Get-FileHash -Algorithm SHA256` 与同版本前缀的 `*-SHA256SUMS.txt` 比较。

当前交付仍是未签名便携测试包；预发布 Release 不代表已经取得应用签名，也不启用正式自动更新或绕过 TUN 服务信任校验。`testaccount.txt` 保持忽略，不能作为 Actions secret、artifact 或 Release 文件上传。

GitHub 权限与发布语义参照：[GITHUB_TOKEN](https://docs.github.com/en/actions/tutorials/authenticate-with-github_token)、[Release API](https://docs.github.com/en/rest/releases/releases)。

统一发布器读取 main 分支运行列表，再逐条核验成功状态与来源，避免 GitHub 状态筛选索引滞后漏掉新包。以 preview 页面逐端 commit/run 和附件摘要确认更新；补发使用共享发布手册中的统一工作流。

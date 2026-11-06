# Windows 本地构建与 GitHub Release

## 目录与入口

仓库按平台组织：`Windows/`、`Android/`、`Mac/`、`iOS/`、`HarmonyOS/`；`client-api/` 为服务端，`client-config/` 为远程客户端配置，`assets/` 为公共素材，`docs/` 为共享文档。GitHub 工作流位于根目录 `.github/workflows/`。

Windows 主入口是 `Windows/scripts/package.ps1`。本地与 GitHub 都运行这个脚本，依次执行业务包/Flutter 分析及测试、Go 竞态测试与 vet、契约检查、原生与 Flutter 构建、解压文件校验、便携助手测试。真实账号联网、TUN/WFP 接管及签名安装不属于 CI 检查。

## 本地生成测试客户端

安装 `Windows/toolchain.json` 指定的 Flutter、Go，以及 Visual Studio 2022 C++ 桌面开发工具和 Windows SDK。PATH 需能找到 `gcc`、`windres`；本机原有 MSYS2 UCRT64 路径可作为后备。CPython 不可使用 MSYS Python。

在仓库根目录执行：

```powershell
$env:BLUEGRASS_PYTHON = '你的 CPython python.exe 完整路径'
& $env:BLUEGRASS_PYTHON -m pip install -r Windows/contracts/requirements-test.txt
./Windows/scripts/package.ps1
```

不指定版本时读取 `Windows/release.json` 的 `version` 和 `buildBase`。也可以显式指定，例如 `./Windows/scripts/package.ps1 -Version 0.1.0-beta.1 -Build 22`。`build.ps1` 仍保留用于开发时仅构建；CI 必须运行 `package.ps1`。

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

触发路径为 `Windows/**`、Windows 工作流本身、根 `.gitignore` 和 `.gitattributes`。其他平台或服务端的独立改动不重复生成 Windows Release。原有 macOS 工作流仍独立运行；共享 Windows 原生/Flutter 代码变化可能同时触发两端检查。

云端构建号为 `buildBase + github.run_number`，例如 base=22、首次运行编号=1，客户端版本为 `0.1.0-beta.1+23`。标签为 `windows-v0.1.0-beta.1-build.23-r1`；同一运行重跑的 r2 使用新标签，不覆盖已发布版本。构建号必须小于等于 65535，超出则失败，不能循环归零。不要重置已有工作流的编号后复用既有标签。

构建 job 只有仓库读取权限，发布 job 才拥有 `contents: write`，使用自动提供的 `GITHUB_TOKEN`，不需要个人访问令牌或测试账号。发布 job 仅取当前运行的确切 artifact，复核哈希与来源提交，先建立草稿并上传三项资产，再公开为预发布 Release。构建失败不会发布；上传或发布中断可能保留草稿，确认失败后可删除该草稿，重跑会生成新的 r 标签。

下载入口：[GitHub Releases](https://github.com/BlueGrassN/BlueGrass/releases)。当前仓库私有，下载者需要仓库访问权限。本流程不修改仓库可见性。

## 下载和检查

下载 Release 中的 ZIP 并完整解压，运行最外层 `BlueGrass.exe`，保留同级 `runtime/`。不要只复制 EXE。可用 `Get-FileHash -Algorithm SHA256` 与 `SHA256SUMS.txt` 比较。

当前交付仍是未签名便携测试包；预发布 Release 不代表已经取得应用签名，也不启用正式自动更新或绕过 TUN 服务信任校验。`testaccount.txt` 保持忽略，不能作为 Actions secret、artifact 或 Release 文件上传。

GitHub 权限与发布语义参照：[GITHUB_TOKEN](https://docs.github.com/en/actions/tutorials/authenticate-with-github_token)、[Release API](https://docs.github.com/en/rest/releases/releases)。

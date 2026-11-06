# BlueGrass Flutter Windows app

Windows 薄 Flutter 入口与 C++ 宿主。UI/业务实现来自根 `packages/client`（`bluegrass_client`），本宿主包名为 `bluegrass_windows`；`lib/` 的兼容导出不维护独立实现。运行、构建、测试和交付边界见 [Windows 开发说明](../README.md)。

- `lib/main.dart`：真实 API 入口。
- `lib/main_fixture.dart`：明确标注模拟环境的开发入口，禁止 release。
- `windows/runner/bluegrass_platform.cpp`：DPAPI、托盘与平台操作。
- `integration_test/windows_test.dart`：真实 Windows 安全存储与界面流程测试。

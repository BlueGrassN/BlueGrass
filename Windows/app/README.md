# BlueGrass Flutter Windows app

Windows 客户端界面与 C++ 宿主。运行、构建、测试和交付边界见 [Windows 开发说明](../README.md)。

- `lib/main.dart`：真实 API 入口。
- `lib/main_fixture.dart`：明确标注模拟环境的开发入口，禁止 release。
- `windows/runner/bluegrass_platform.cpp`：DPAPI、托盘与平台操作。
- `integration_test/windows_test.dart`：真实 Windows 安全存储与界面流程测试。

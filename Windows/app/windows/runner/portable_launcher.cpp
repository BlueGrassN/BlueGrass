#include <windows.h>

#include <filesystem>
#include <string>

// This small, statically linked entry point needs no adjacent runtime DLLs.
// The real client and its helpers stay together, preserving IPC/trust paths.
int APIENTRY wWinMain(HINSTANCE, HINSTANCE, wchar_t* arguments, int show) {
  std::wstring executable(32768, L'\0');
  const DWORD size = GetModuleFileNameW(nullptr, executable.data(),
                                       static_cast<DWORD>(executable.size()));
  if (!size || size >= executable.size()) return ERROR_BAD_PATHNAME;
  executable.resize(size);
  const auto directory = std::filesystem::path(executable).parent_path() / L"runtime";
  const auto client = directory / L"BlueGrass.exe";
  std::wstring command = L"\"" + client.wstring() + L"\"";
  // lpCmdLine already excludes the launcher name. Preserve its quoting exactly,
  // including empty arguments, Unicode, toast activation and --autostart.
  if (arguments && *arguments) command += std::wstring(L" ") + arguments;
  STARTUPINFOW startup{};
  startup.cb = sizeof(startup);
  startup.dwFlags = STARTF_USESHOWWINDOW;
  startup.wShowWindow = static_cast<WORD>(show);
  PROCESS_INFORMATION process{};
  if (!CreateProcessW(client.c_str(), command.data(), nullptr, nullptr, FALSE, 0,
                      nullptr, directory.c_str(), &startup, &process)) {
    const DWORD error = GetLastError();
    MessageBoxW(nullptr,
                L"无法启动 BlueGrass。请将整个压缩包重新解压，保留主程序旁的 runtime 文件夹。",
                L"BlueGrass", MB_OK | MB_ICONERROR);
    return static_cast<int>(error);
  }
  CloseHandle(process.hThread);
  CloseHandle(process.hProcess);
  return 0;
}

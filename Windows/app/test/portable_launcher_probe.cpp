#include <windows.h>
#include <filesystem>
#include <fstream>
#include <string>

// Synthetic child process used to verify the real launcher's argument and CWD
// handling. It does not load user settings or touch the network.
int wmain(int argc, wchar_t** argv) {
  wchar_t executable[32768]{}, directory[32768]{};
  if (!GetModuleFileNameW(nullptr, executable, 32768) ||
      !GetCurrentDirectoryW(32768, directory)) return 1;
  std::wstring record = std::wstring(directory) + L'\n';
  for (int i = 1; i < argc; ++i) record += std::wstring(argv[i]) + L'\n';
  std::ofstream output(std::filesystem::path(executable).parent_path() / L"launch.txt",
                       std::ios::binary);
  output.write(reinterpret_cast<const char*>(record.data()),
               static_cast<std::streamsize>(record.size() * sizeof(wchar_t)));
  return output ? 0 : 1;
}

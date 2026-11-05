#ifndef BLUEGRASS_PORTABLE_PATHS_H_
#define BLUEGRASS_PORTABLE_PATHS_H_

#include <windows.h>
#include <filesystem>
#include <string>

namespace bluegrass {
// Only user-facing launch entries use the outer executable. Helpers and COM
// activation continue to address the real client inside runtime directly.
inline std::wstring PublicExecutable(const std::wstring& executable) {
  const std::filesystem::path path(executable);
  if (_wcsicmp(path.filename().c_str(), L"BlueGrass.exe") != 0 ||
      _wcsicmp(path.parent_path().filename().c_str(), L"runtime") != 0) {
    return executable;
  }
  const auto launcher = path.parent_path().parent_path() / L"BlueGrass.exe";
  const auto attributes = GetFileAttributesW(launcher.c_str());
  if (attributes == INVALID_FILE_ATTRIBUTES || (attributes & FILE_ATTRIBUTE_DIRECTORY)) {
    return executable;
  }
  return launcher.wstring();
}
}  // namespace bluegrass
#endif

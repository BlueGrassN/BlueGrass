#ifndef BLUEGRASS_OWNED_AUTOSTART_H_
#define BLUEGRASS_OWNED_AUTOSTART_H_

#include <windows.h>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>
#include "portable_paths.h"

namespace bluegrass {
inline constexpr wchar_t kRunKey[] = L"Software\\Microsoft\\Windows\\CurrentVersion\\Run";
inline constexpr wchar_t kRunName[] = L"BlueGrass";

inline std::wstring AutostartCommand() {
  std::vector<wchar_t> path(32768);
  const DWORD length = GetModuleFileNameW(nullptr, path.data(), static_cast<DWORD>(path.size()));
  if (!length || length >= path.size()) throw std::runtime_error("module_path_failed");
  const auto executable = PublicExecutable(std::wstring(path.data(), length));
  if (executable.find_first_of(L"\"\r\n") != std::wstring::npos) {
    throw std::runtime_error("invalid_module_path");
  }
  return L"\"" + executable + L"\" --autostart";
}

inline std::optional<std::wstring> ReadRunValue(HKEY key, const std::wstring& name) {
  DWORD type = 0, bytes = 0;
  auto status = RegQueryValueExW(key, name.c_str(), nullptr, &type, nullptr, &bytes);
  if (status == ERROR_FILE_NOT_FOUND) return std::nullopt;
  if (status != ERROR_SUCCESS || type != REG_SZ || bytes > 65536 || bytes % sizeof(wchar_t)) {
    throw std::runtime_error("unowned_autostart_value");
  }
  std::vector<wchar_t> value(bytes / sizeof(wchar_t) + 1, L'\0');
  status = RegQueryValueExW(key, name.c_str(), nullptr, &type,
                          reinterpret_cast<BYTE*>(value.data()), &bytes);
  if (status != ERROR_SUCCESS || type != REG_SZ || bytes < sizeof(wchar_t) ||
      value[bytes / sizeof(wchar_t) - 1] != L'\0') {
    throw std::runtime_error("autostart_read_failed");
  }
  std::wstring result(value.data(), bytes / sizeof(wchar_t) - 1);
  if (result.find(L'\0') != std::wstring::npos) throw std::runtime_error("unowned_autostart_value");
  return result;
}

inline void SetOwnedAutostart(HKEY key, const std::wstring& name,
                             const std::wstring& command, bool enabled) {
  const auto previous = ReadRunValue(key, name);
  // A same-name entry for a different install or executable is never ours.
  if (previous && *previous != command) throw std::runtime_error("unowned_autostart_value");
  if (enabled) {
    const auto bytes = static_cast<DWORD>((command.size() + 1) * sizeof(wchar_t));
    if (RegSetValueExW(key, name.c_str(), 0, REG_SZ,
        reinterpret_cast<const BYTE*>(command.c_str()), bytes) != ERROR_SUCCESS) {
      throw std::runtime_error("autostart_write_failed");
    }
  } else if (previous && RegDeleteValueW(key, name.c_str()) != ERROR_SUCCESS) {
    throw std::runtime_error("autostart_delete_failed");
  }
}

class RunKey {
 public:
  explicit RunKey(bool writable) {
    const LSTATUS status = writable
        ? RegCreateKeyExW(HKEY_CURRENT_USER, kRunKey, 0, nullptr, 0,
                         KEY_QUERY_VALUE | KEY_SET_VALUE, nullptr, &key, nullptr)
        : RegOpenKeyExW(HKEY_CURRENT_USER, kRunKey, 0, KEY_QUERY_VALUE, &key);
    if (status != ERROR_SUCCESS && !(status == ERROR_FILE_NOT_FOUND && !writable)) {
      throw std::runtime_error("autostart_key_failed");
    }
  }
  ~RunKey() { if (key) RegCloseKey(key); }
  RunKey(const RunKey&) = delete;
  RunKey& operator=(const RunKey&) = delete;
  HKEY key = nullptr;
};
}  // namespace bluegrass
#endif

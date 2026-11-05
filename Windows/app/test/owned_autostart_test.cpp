#include <windows.h>
#include <iostream>
#include <string>

#include "../windows/runner/owned_autostart.h"

void require(bool condition) { if (!condition) throw std::runtime_error("autostart_test_failed"); }

void Verify() {
  HKEY key = nullptr;
  require(RegCreateKeyExW(HKEY_CURRENT_USER, bluegrass::kRunKey, 0, nullptr, 0,
                        KEY_QUERY_VALUE | KEY_SET_VALUE, nullptr, &key, nullptr) == ERROR_SUCCESS);
  const auto name = L"BlueGrass.Verification." + std::to_wstring(GetCurrentProcessId());
  DWORD size = 0;
  require(RegQueryValueExW(key, name.c_str(), nullptr, nullptr, nullptr, &size) == ERROR_FILE_NOT_FOUND);
  struct Cleanup {
    HKEY key; std::wstring name;
    ~Cleanup() { RegDeleteValueW(key, name.c_str()); RegCloseKey(key); }
  } cleanup{key, name};
  const auto command = bluegrass::AutostartCommand();
  require(command.front() == L'"' && command.find(L"\" --autostart") != std::wstring::npos);
  wchar_t actual[32768]{};
  require(GetModuleFileNameW(nullptr, actual, 32768) != 0);
  const std::filesystem::path current(actual);
  if (_wcsicmp(current.parent_path().filename().c_str(), L"runtime") == 0) {
    const auto launcher = current.parent_path().parent_path() / L"BlueGrass.exe";
    require(command == L"\"" + launcher.wstring() + L"\" --autostart");
    require(bluegrass::PublicExecutable((current.parent_path() / L"Other.exe").wstring()) ==
        (current.parent_path() / L"Other.exe").wstring());
  }
  bluegrass::SetOwnedAutostart(key, name, command, true);
  require(bluegrass::ReadRunValue(key, name) == command);
  bluegrass::SetOwnedAutostart(key, name, command, true);
  bool refused = false;
  try { bluegrass::SetOwnedAutostart(key, name, L"\"C:\\Other App\\other.exe\" --autostart", false); }
  catch (...) { refused = true; }
  require(refused && bluegrass::ReadRunValue(key, name) == command);
  refused = false;
  try { bluegrass::SetOwnedAutostart(key, name, L"\"C:\\Other App\\other.exe\" --autostart", true); }
  catch (...) { refused = true; }
  require(refused && bluegrass::ReadRunValue(key, name) == command);
  bluegrass::SetOwnedAutostart(key, name, command, false);
  require(!bluegrass::ReadRunValue(key, name).has_value());
  bluegrass::SetOwnedAutostart(key, name, command, false);
  std::cout << "PASS: exact quoted path, idempotent enable/disable, foreign value preserved; scoped Run value removed\n";
}

int main() {
  try { Verify(); return 0; }
  catch (const std::exception& error) { std::cerr << error.what() << '\n'; return 1; }
}

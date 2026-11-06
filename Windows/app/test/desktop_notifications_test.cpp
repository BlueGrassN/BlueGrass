#include "../windows/runner/desktop_notifications.h"
#include "../windows/runner/portable_paths.h"
#include <NotificationActivationCallback.h>
#include <wrl.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.UI.Notifications.h>
#include <iostream>
#include <stdexcept>
#include <filesystem>
#include <fstream>
#include <functional>
#include <shlobj.h>

void Check(bool value, const char* message) {
  if (!value) throw std::runtime_error(message);
}

void CheckShortcutRelocation(bluegrass::DesktopNotifications& backend,
                             const bluegrass::ToastIdentity& identity) {
  wchar_t actual_executable[32768]{}, clsid[40]{};
  Check(GetModuleFileNameW(nullptr, actual_executable, 32768) != 0, "missing executable");
  const auto executable = bluegrass::PublicExecutable(actual_executable);
  StringFromGUID2(identity.activator, clsid, 40);
  const auto server_path = std::wstring(L"Software\\Classes\\CLSID\\") + clsid + L"\\LocalServer32";
  HKEY server = nullptr;
  Check(RegOpenKeyExW(HKEY_CURRENT_USER, server_path.c_str(), 0, KEY_READ | KEY_WRITE, &server) == ERROR_SUCCESS, "missing test registration");
  struct CloseKey { HKEY key; ~CloseKey() { RegCloseKey(key); } } close{server};
  PWSTR programs = nullptr;
  winrt::check_hresult(SHGetKnownFolderPath(FOLDERID_Programs, 0, nullptr, &programs));
  const auto shortcut = std::filesystem::path(programs) / identity.shortcut_name;
  CoTaskMemFree(programs);
  Microsoft::WRL::ComPtr<IShellLinkW> link;
  winrt::check_hresult(CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&link)));
  Microsoft::WRL::ComPtr<IPersistFile> file;
  winrt::check_hresult(link.As(&file));
  winrt::check_hresult(file->Load(shortcut.c_str(), STGM_READWRITE));
  const auto set_target = [&](const std::wstring& target, bool change_link = true, bool change_server = true) {
    if (change_link) {
      winrt::check_hresult(link->SetPath(target.c_str()));
      winrt::check_hresult(link->SetIconLocation(target.c_str(), 0));
      winrt::check_hresult(file->Save(shortcut.c_str(), TRUE));
    }
    if (change_server) {
      const auto command = L"\"" + target + L"\" --toast-activated";
      Check(RegSetValueExW(server, nullptr, 0, REG_SZ,
          reinterpret_cast<const BYTE*>(command.c_str()),
          static_cast<DWORD>((command.size() + 1) * sizeof(wchar_t))) == ERROR_SUCCESS, "cannot update test registration");
    }
  };
  const auto old_dir = std::filesystem::temp_directory_path() /
      (L"BlueGrassRegistrationTest-" + std::to_wstring(GetCurrentProcessId()) + L"-" + std::to_wstring(GetTickCount64()));
  const auto old_executable = old_dir / std::filesystem::path(executable).filename();
  struct Restore {
    std::function<void()> restore;
    std::filesystem::path file, directory;
    ~Restore() {
      try { restore(); } catch (...) {}
      std::error_code error;
      std::filesystem::remove(file, error);
      std::filesystem::remove(directory, error);
    }
  } restore{[&] {
      set_target(executable, true, false);
      set_target(actual_executable, false, true);
    }, old_executable, old_dir};
  if (executable != actual_executable) {
    auto previous_case = executable;
    CharLowerBuffW(previous_case.data(), static_cast<DWORD>(previous_case.size()));
    set_target(previous_case);
    Check(backend.EnsureRegistration(), "flat-to-runtime activation migration failed");
    Check(backend.HasOwnedRegistration(), "portable registration does not match");
    wchar_t migrated[32768]{};
    DWORD bytes = sizeof(migrated);
    Check(RegQueryValueExW(server, nullptr, nullptr, nullptr,
        reinterpret_cast<BYTE*>(migrated), &bytes) == ERROR_SUCCESS, "missing migrated activation");
    Check(std::wstring(migrated) == std::wstring(L"\"") + actual_executable + L"\" --toast-activated",
          "COM activation must address the real server, not the launcher");
    std::cout << "PASS: public shortcut retained; COM activation migrated to runtime\n";
  }
  std::cout << "CHECK: retired shortcut and activation path\n";
  set_target(old_executable.wstring());
  Check(backend.EnsureRegistration(), "missing former executable was not repaired");
  Check(backend.HasOwnedRegistration(), "repaired registration does not belong to current executable");
  file.Reset(); link.Reset();
  winrt::check_hresult(CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&link)));
  winrt::check_hresult(link.As(&file));
  winrt::check_hresult(file->Load(shortcut.c_str(), STGM_READWRITE));
  wchar_t icon[32768]{}; int index = -1;
  winrt::check_hresult(link->GetIconLocation(icon, 32768, &index));
  Check(_wcsicmp(icon, executable.c_str()) == 0 && index == 0, "shortcut icon still points to old executable");
  set_target(old_executable.wstring(), true, false);
  Check(backend.EnsureRegistration(), "partial shortcut repair failed");
  set_target(old_executable.wstring(), false, true);
  Check(backend.EnsureRegistration(), "partial activator repair failed");
  Check(std::filesystem::create_directory(old_dir), "test directory already exists");
  std::ofstream(old_executable).put('x');
  Check(std::filesystem::exists(old_executable), "test file unavailable");
  set_target(old_executable.wstring());
  Check(!backend.EnsureRegistration(), "existing installation was taken over");
  std::cout << "PASS: missing-path repair, explicit icon, partial repair and live-install preservation\n";
}

int wmain(int argc, wchar_t** argv) {
  // Match the real Flutter runner's COM initialization, including shutdown order.
  winrt::check_hresult(CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED));
  struct Apartment { ~Apartment() { CoUninitialize(); } } apartment;
  try {
    const auto first = bluegrass::ToastAddress(L"same-id");
    Check(first == bluegrass::ToastAddress(L"same-id"), "address unstable");
    Check(first != bluegrass::ToastAddress(L"other-id"), "address collision");
    Check(first.first.size() == 16 && first.second.size() == 16, "address too long");
    const auto xml = bluegrass::ToastXml(L"id<&\"'", L"Title <safe>", L"Body & safe");
    winrt::Windows::Data::Xml::Dom::XmlDocument doc;
    doc.LoadXml(xml);
    Check(doc.DocumentElement().GetAttribute(L"launch") == L"id<&\"'", "launch escaped incorrectly");
    Check(doc.GetElementsByTagName(L"text").Item(0).InnerText() == L"Title <safe>", "title escaped incorrectly");
    Check(doc.GetElementsByTagName(L"audio").Item(0).as<winrt::Windows::Data::Xml::Dom::XmlElement>().GetAttribute(L"silent") == L"true", "toast not silent");
    std::cout << "PASS: stable tag/group, XML escaping, silent payload\n";
    {
      bluegrass::ToastIdentity identity;
      identity.app_id = L"com.bluegrass.verification.activation";
      identity.shortcut_name = L"BlueGrass Verification Activation.lnk";
      winrt::check_hresult(CoCreateGuid(&identity.activator));
      bluegrass::DesktopNotifications backend(nullptr, 0, identity);
      Microsoft::WRL::ComPtr<IClassFactory> factory;
      winrt::check_hresult(CoGetClassObject(identity.activator, CLSCTX_LOCAL_SERVER,
                                            nullptr, IID_PPV_ARGS(&factory)));
      Microsoft::WRL::ComPtr<INotificationActivationCallback> callback;
      winrt::check_hresult(factory->CreateInstance(nullptr, IID_PPV_ARGS(&callback)));
      Check(callback->Activate(L"unrelated.application", L"click", nullptr, 0) == E_INVALIDARG,
            "foreign application activation accepted");
      Check(!backend.TakeActivation(), "rejected activation leaked");
      winrt::check_hresult(callback->Activate(identity.app_id.c_str(), L"queued-click", nullptr, 0));
      Check(backend.TakeActivation() == L"queued-click", "cold activation lost before consumer ready");
      Check(!backend.TakeActivation(), "activation delivered twice");
      std::cout << "PASS: real COM callback, app identity guard, queued activation drain\n";
    }
    if (argc == 2 && (std::wstring(argv[1]) == L"--smoke" || std::wstring(argv[1]) == L"--registration")) {
      const auto suffix = std::to_wstring(GetCurrentProcessId()) + L"." + std::to_wstring(GetTickCount64());
      bluegrass::ToastIdentity identity;
      identity.app_id = L"com.bluegrass.verification." + suffix;
      identity.shortcut_name = L"BlueGrass Verification " + suffix + L".lnk";
      winrt::check_hresult(CoCreateGuid(&identity.activator));
      bluegrass::DesktopNotifications backend(nullptr, 0, identity);
      struct Cleanup {
        bluegrass::DesktopNotifications& backend;
        ~Cleanup() {
          try { backend.Cancel(L"generic-smoke"); } catch (...) {}
          try { backend.RemoveOwnedRegistration(); } catch (...) {}
        }
      } cleanup{backend};
      // Verification identity only: no production keys or notification settings.
      Check(backend.EnsureRegistration(), "verification registration failed");
      Check(backend.EnsureRegistration(), "registration is not idempotent");
      CheckShortcutRelocation(backend, identity);
      wchar_t clsid[40]{};
      StringFromGUID2(identity.activator, clsid, 40);
      const auto server_path = std::wstring(L"Software\\Classes\\CLSID\\") + clsid + L"\\LocalServer32";
      HKEY server = nullptr;
      Check(RegOpenKeyExW(HKEY_CURRENT_USER, server_path.c_str(), 0, KEY_READ | KEY_WRITE, &server) == ERROR_SUCCESS, "test registration missing");
      wchar_t saved[32768]{};
      DWORD saved_size = sizeof(saved);
      Check(RegQueryValueExW(server, nullptr, nullptr, nullptr, reinterpret_cast<BYTE*>(saved), &saved_size) == ERROR_SUCCESS, "cannot read test registration");
      const wchar_t foreign[] = L"\"C:\\unrelated-verification.exe\"";
      Check(RegSetValueExW(server, nullptr, 0, REG_SZ, reinterpret_cast<const BYTE*>(foreign), sizeof(foreign)) == ERROR_SUCCESS, "cannot simulate different owner");
      const bool refused_foreign = !backend.EnsureRegistration();
      // Always restore the exact test-owned value before evaluating refusal.
      const auto restored = RegSetValueExW(server, nullptr, 0, REG_SZ, reinterpret_cast<BYTE*>(saved), saved_size);
      RegCloseKey(server);
      Check(restored == ERROR_SUCCESS && refused_foreign, "different executable registration was overwritten");
      std::cout << "PASS: refuses different executable registration without takeover\n";
      if (std::wstring(argv[1]) == L"--registration") {
        Check(backend.RemoveOwnedRegistration(), "verification cleanup failed");
        Check(!backend.HasOwnedRegistration(), "verification registration remains");
        std::cout << "PASS: isolated shell registration cleaned up\n";
        return 0;
      }
      try {
        const auto setting = winrt::Windows::UI::Notifications::ToastNotificationManager::CreateToastNotifier(identity.app_id).Setting();
        std::cout << "INFO: NotificationSetting=" << static_cast<int>(setting)
                  << " (0=Enabled,1=DisabledForApplication,2=DisabledForUser,3=DisabledByGroupPolicy,4=DisabledByManifest)\n";
      } catch (const winrt::hresult_error& error) {
        if (error.code() != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) throw;
        std::cout << "INFO: first-run unpackaged settings profile absent (0x80070490)\n";
      }
      const bool accepted = backend.Submit(L"generic-smoke", L"BlueGrass verification", L"Temporary local notification test", true, 15);
      if (accepted) {
        Check(backend.Submit(L"generic-smoke", L"BlueGrass verification", L"Temporary replacement test", true, 15), "replacement rejected");
        const auto history = winrt::Windows::UI::Notifications::ToastNotificationManager::History().GetHistory(identity.app_id);
        const auto address = bluegrass::ToastAddress(L"generic-smoke");
        unsigned count = 0;
        for (const auto& toast : history) {
          if (toast.Tag() == address.first && toast.Group() == address.second) ++count;
        }
        Check(count <= 1, "stable ID created duplicate notifications");
        std::cout << "PASS: real WinRT submission and replacement accepted; matching history=" << count << "\n";
      } else {
        std::cout << "SKIP: Windows notification permission/policy/backend rejected smoke\n";
      }
      backend.Cancel(L"generic-smoke");
      backend.Cancel(L"generic-smoke");
      for (const auto& toast : winrt::Windows::UI::Notifications::ToastNotificationManager::History().GetHistory(identity.app_id)) {
        const auto address = bluegrass::ToastAddress(L"generic-smoke");
        Check(toast.Tag() != address.first || toast.Group() != address.second, "cancel did not remove notification");
      }
      Check(backend.RemoveOwnedRegistration(), "verification cleanup failed");
      Check(!backend.HasOwnedRegistration(), "verification registration remains");
      std::cout << "PASS: cancel twice; no matching notification; test registration removed\n";
    }
    return 0;
  } catch (const winrt::hresult_error& error) {
    std::cerr << "FAIL: Windows notification test HRESULT=" << std::hex << error.code().value << "\n";
  } catch (const std::exception& error) {
    std::cerr << "FAIL: " << error.what() << "\n";
  }
  return 1;
}

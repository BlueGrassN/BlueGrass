#ifndef BLUEGRASS_DESKTOP_NOTIFICATIONS_H_
#define BLUEGRASS_DESKTOP_NOTIFICATIONS_H_

#include <windows.h>
#include <memory>
#include <optional>
#include <string>
#include <utility>

namespace bluegrass {
// Installer contract: apply both properties to the Start Menu shortcut and
// register CLSID\{...}\LocalServer32 with the same quoted executable path.
inline constexpr wchar_t kAppUserModelId[] = L"com.bluegrass.client";
inline constexpr wchar_t kToastActivatorClsid[] = L"{9221CCD9-CFE9-404B-BBB4-CDA9CA8BB484}";
inline constexpr CLSID kToastActivator = {
    0x9221ccd9, 0xcfe9, 0x404b, {0xbb, 0xb4, 0xcd, 0xa9, 0xca, 0x8b, 0xb4, 0x84}};

struct ToastIdentity {
  std::wstring app_id = kAppUserModelId;
  CLSID activator = kToastActivator;
  std::wstring shortcut_name = L"BlueGrass.lnk";
};

std::pair<std::wstring, std::wstring> ToastAddress(const std::wstring& id);
std::wstring ToastXml(const std::wstring& id, const std::wstring& title,
                      const std::wstring& body);

class DesktopNotifications {
 public:
  DesktopNotifications(HWND window, UINT activation_message,
                       ToastIdentity identity = {});
  ~DesktopNotifications();
  DesktopNotifications(const DesktopNotifications&) = delete;
  DesktopNotifications& operator=(const DesktopNotifications&) = delete;

  bool Submit(const std::wstring& id, const std::wstring& title,
              const std::wstring& body, bool suppress_popup = false,
              int lifetime_seconds = 7 * 24 * 60 * 60);
  void Cancel(const std::wstring& id);
  std::optional<std::wstring> TakeActivation();
  bool EnsureRegistration();
  bool HasOwnedRegistration() const;
  // Only a com.bluegrass.verification.* identity can use this test cleanup.
  bool RemoveOwnedRegistration();

 private:
  class Impl;
  std::unique_ptr<Impl> impl_;
};
}  // namespace bluegrass
#endif

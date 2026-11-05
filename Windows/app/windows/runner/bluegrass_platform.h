#ifndef BLUEGRASS_PLATFORM_H_
#define BLUEGRASS_PLATFORM_H_

#include <flutter/method_channel.h>
#include <flutter/standard_method_codec.h>
#include <windows.h>
#include <shellapi.h>
#include <memory>
#include <optional>
#include "desktop_notifications.h"
#include "control_http_bridge.h"

class BlueGrassPlatform {
 public:
  BlueGrassPlatform(flutter::BinaryMessenger* messenger, HWND window);
  ~BlueGrassPlatform();
  std::optional<LRESULT> Handle(UINT message, WPARAM wparam, LPARAM lparam);
 private:
  struct WindowChrome {
    COLORREF caption;
    COLORREF text;
    bool dark;
  };
  void RegisterTray();
  void ApplyChrome();
  void HandleCall(const flutter::MethodCall<flutter::EncodableValue>& call,
                  std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result);
  HWND window_;
  NOTIFYICONDATAW tray_{};
  bool tray_available_ = false;
  bool closing_ = false;
  UINT taskbar_created_;
  std::optional<WindowChrome> chrome_;
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> channel_;
  std::unique_ptr<bluegrass::DesktopNotifications> notifications_;
  std::unique_ptr<bluegrass::ControlHttpBridge> control_http_;
};
#endif

#include "bluegrass_platform.h"

#include <dwmapi.h>
#include <wincrypt.h>
#include <shlobj.h>
#include <filesystem>
#include <fstream>
#include <stdexcept>
#include <string>
#include <vector>
#include "resource.h"
#include "owned_autostart.h"

namespace {
using flutter::EncodableMap;
using flutter::EncodableValue;
constexpr UINT kTrayMessage = WM_APP + 41;
constexpr UINT kExitMessage = WM_APP + 42;
constexpr UINT kNotificationMessage = WM_APP + 43;
constexpr UINT kControlHttpMessage = WM_APP + 44;
constexpr UINT kChromeMessage = WM_APP + 45;
// DWM attributes by value so older SDK headers still compile. Caption and
// text colours exist from Windows 11; earlier versions reject them harmlessly.
constexpr DWORD kDarkModeAttribute = 20;
constexpr DWORD kCaptionColorAttribute = 35;
constexpr DWORD kTextColorAttribute = 36;
constexpr size_t kMaxRecord = 1024 * 1024;

std::string Argument(const EncodableValue* arguments, const char* name) {
  if (!arguments) throw std::runtime_error("invalid_argument");
  const auto* map = std::get_if<EncodableMap>(arguments);
  if (!map) throw std::runtime_error("invalid_argument");
  const auto it = map->find(EncodableValue(name));
  if (it == map->end() || !std::holds_alternative<std::string>(it->second)) {
    throw std::runtime_error("invalid_argument");
  }
  return std::get<std::string>(it->second);
}

int64_t IntegerArgument(const EncodableValue* arguments, const char* name) {
  const auto* map = arguments ? std::get_if<EncodableMap>(arguments) : nullptr;
  if (!map) throw std::runtime_error("invalid_argument");
  const auto it = map->find(EncodableValue(name));
  if (it == map->end()) throw std::runtime_error("invalid_argument");
  if (const auto* value = std::get_if<int32_t>(&it->second)) return *value;
  if (const auto* value = std::get_if<int64_t>(&it->second)) return *value;
  throw std::runtime_error("invalid_argument");
}

bool BoolArgument(const EncodableValue* arguments, const char* name) {
  const auto* map = arguments ? std::get_if<EncodableMap>(arguments) : nullptr;
  if (!map) throw std::runtime_error("invalid_argument");
  const auto it = map->find(EncodableValue(name));
  if (it == map->end() || !std::holds_alternative<bool>(it->second)) {
    throw std::runtime_error("invalid_argument");
  }
  return std::get<bool>(it->second);
}

COLORREF ColorFromArgb(int64_t argb) {
  return RGB((argb >> 16) & 0xFF, (argb >> 8) & 0xFF, argb & 0xFF);
}

std::wstring Wide(const std::string& value) {
  const int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS,
                                        value.data(), static_cast<int>(value.size()), nullptr, 0);
  if (!length && !value.empty()) throw std::runtime_error("invalid_utf8");
  std::wstring out(length, L'\0');
  MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                      static_cast<int>(value.size()), out.data(), length);
  return out;
}

std::string Narrow(const std::wstring& value) {
  const int length = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS,
      value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
  if (!length && !value.empty()) throw std::runtime_error("invalid_unicode");
  std::string out(length, '\0');
  WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(),
      static_cast<int>(value.size()), out.data(), length, nullptr, nullptr);
  return out;
}

std::filesystem::path RecordPath(const std::string& key) {
  if (key.empty() || key.size() > 100 ||
      key.find_first_not_of("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != std::string::npos) {
    throw std::runtime_error("invalid_record_key");
  }
  PWSTR local = nullptr;
  if (FAILED(SHGetKnownFolderPath(FOLDERID_LocalAppData, KF_FLAG_CREATE, nullptr, &local))) {
    throw std::runtime_error("storage_unavailable");
  }
  auto path = std::filesystem::path(local) / L"BlueGrass" / L"client";
  CoTaskMemFree(local);
  std::filesystem::create_directories(path);
  return path / (Wide(key) + L".dpapi");
}

void WriteRecord(const std::filesystem::path& path, std::string value) {
  if (value.size() > kMaxRecord) throw std::runtime_error("record_too_large");
  DATA_BLOB input{static_cast<DWORD>(value.size()), reinterpret_cast<BYTE*>(value.data())};
  DATA_BLOB output{};
  const BOOL protected_ok = CryptProtectData(&input, L"BlueGrass", nullptr, nullptr,
                                              nullptr, CRYPTPROTECT_UI_FORBIDDEN, &output);
  SecureZeroMemory(value.data(), value.size());
  if (!protected_ok) throw std::runtime_error("protect_failed");
  const auto temporary = path.wstring() + L".tmp";
  HANDLE file = CreateFileW(temporary.c_str(), GENERIC_WRITE, 0, nullptr,
                             CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
  if (file == INVALID_HANDLE_VALUE) {
    LocalFree(output.pbData);
    throw std::runtime_error("storage_write_failed");
  }
  DWORD written = 0;
  bool ok = WriteFile(file, output.pbData, output.cbData, &written, nullptr) &&
            written == output.cbData && FlushFileBuffers(file);
  CloseHandle(file);
  LocalFree(output.pbData);
  if (ok) ok = MoveFileExW(temporary.c_str(), path.c_str(),
                           MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH) != 0;
  if (!ok) {
    DeleteFileW(temporary.c_str());
    throw std::runtime_error("storage_write_failed");
  }
}

std::optional<std::string> ReadRecord(const std::filesystem::path& path) {
  if (!std::filesystem::exists(path)) return std::nullopt;
  const auto size = std::filesystem::file_size(path);
  if (size > kMaxRecord + 4096) throw std::runtime_error("record_too_large");
  std::ifstream stream(path, std::ios::binary);
  std::vector<BYTE> data(static_cast<size_t>(size));
  if (!stream.read(reinterpret_cast<char*>(data.data()), static_cast<std::streamsize>(size))) {
    throw std::runtime_error("storage_read_failed");
  }
  DATA_BLOB input{static_cast<DWORD>(data.size()), data.data()};
  DATA_BLOB output{};
  if (!CryptUnprotectData(&input, nullptr, nullptr, nullptr, nullptr,
                           CRYPTPROTECT_UI_FORBIDDEN, &output)) {
    throw std::runtime_error("unprotect_failed");
  }
  std::string value(reinterpret_cast<char*>(output.pbData), output.cbData);
  SecureZeroMemory(output.pbData, output.cbData);
  LocalFree(output.pbData);
  return value;
}
}

BlueGrassPlatform::BlueGrassPlatform(flutter::BinaryMessenger* messenger, HWND window)
    : window_(window), taskbar_created_(RegisterWindowMessageW(L"TaskbarCreated")) {
  channel_ = std::make_unique<flutter::MethodChannel<EncodableValue>>(
      messenger, "bluegrass/platform", &flutter::StandardMethodCodec::GetInstance());
  channel_->SetMethodCallHandler([this](const auto& call, auto result) {
    HandleCall(call, std::move(result));
  });
  control_http_ = std::make_unique<bluegrass::ControlHttpBridge>(window_, kControlHttpMessage);
  try {
    notifications_ = std::make_unique<bluegrass::DesktopNotifications>(window_, kNotificationMessage);
    // Taskbar identity/icon registration is needed even before login or a toast.
    notifications_->EnsureRegistration();
  } catch (...) { /* Notification availability never gates the main window. */ }
  RegisterTray();
}

BlueGrassPlatform::~BlueGrassPlatform() {
  control_http_.reset();
  notifications_.reset();
  channel_->SetMethodCallHandler(nullptr);
  if (tray_available_) Shell_NotifyIconW(NIM_DELETE, &tray_);
}

void BlueGrassPlatform::RegisterTray() {
  tray_.cbSize = sizeof(tray_);
  tray_.hWnd = window_;
  tray_.uID = 1;
  tray_.uFlags = NIF_ICON | NIF_MESSAGE | NIF_TIP;
  tray_.uCallbackMessage = kTrayMessage;
  tray_.hIcon = LoadIcon(GetModuleHandle(nullptr), MAKEINTRESOURCE(IDI_APP_ICON));
  wcscpy_s(tray_.szTip, L"BlueGrass");
  tray_available_ = Shell_NotifyIconW(NIM_ADD, &tray_) != 0;
}

void BlueGrassPlatform::ApplyChrome() {
  if (!chrome_) return;
  // Each attribute is best effort: the window keeps the system caption when
  // DWM rejects one, and nothing here affects closing to the tray.
  BOOL dark = chrome_->dark ? TRUE : FALSE;
  DwmSetWindowAttribute(window_, kDarkModeAttribute, &dark, sizeof(dark));
  DwmSetWindowAttribute(window_, kCaptionColorAttribute, &chrome_->caption,
                        sizeof(chrome_->caption));
  DwmSetWindowAttribute(window_, kTextColorAttribute, &chrome_->text,
                        sizeof(chrome_->text));
}

std::optional<LRESULT> BlueGrassPlatform::Handle(UINT message, WPARAM, LPARAM lparam) {
  if (message == kControlHttpMessage) { if (control_http_) control_http_->Complete(); return 0; }
  if (message == kChromeMessage) { ApplyChrome(); return 0; }
  if ((message == WM_DWMCOLORIZATIONCOLORCHANGED || message == WM_SETTINGCHANGE) && chrome_) {
    // The default handler resets the caption to the system theme first.
    PostMessage(window_, kChromeMessage, 0, 0);
    return std::nullopt;
  }
  if (message == taskbar_created_) { RegisterTray(); return 0; }
  if (message == kExitMessage) { closing_ = true; DestroyWindow(window_); return 0; }
  if (message == kNotificationMessage) {
    ShowWindow(window_, SW_RESTORE);
    SetForegroundWindow(window_);
    // The native queue is drained explicitly after Flutter installs its handler.
    channel_->InvokeMethod("expiryNotificationAvailable", nullptr);
    return 0;
  }
  if (message == WM_CLOSE && !closing_) {
    if (tray_available_) {
      ShowWindow(window_, SW_HIDE);
      try {
        const auto marker = RecordPath("close_hint_seen");
        if (!ReadRecord(marker).has_value()) {
          tray_.uFlags = NIF_INFO;
          wcscpy_s(tray_.szInfoTitle, L"BlueGrass 仍在运行");
          wcscpy_s(tray_.szInfo, L"可在系统托盘打开窗口或选择退出。");
          tray_.dwInfoFlags = NIIF_INFO;
          Shell_NotifyIconW(NIM_MODIFY, &tray_);
          WriteRecord(marker, "true");
        }
      } catch (...) { /* A hint failure must never crash or expose storage. */ }
    } else {
      ShowWindow(window_, SW_RESTORE);
      SetForegroundWindow(window_);
    }
    return 0;
  }
  if (message == kTrayMessage) {
    if (lparam == WM_LBUTTONUP || lparam == WM_LBUTTONDBLCLK) {
      ShowWindow(window_, SW_RESTORE); SetForegroundWindow(window_);
    } else if (lparam == WM_RBUTTONUP) {
      HMENU menu = CreatePopupMenu();
      AppendMenuW(menu, MF_STRING, 1, L"打开 BlueGrass");
      AppendMenuW(menu, MF_STRING, 3, L"连接 / 断开");
      AppendMenuW(menu, MF_SEPARATOR, 0, nullptr);
      AppendMenuW(menu, MF_STRING, 2, L"退出");
      POINT point; GetCursorPos(&point); SetForegroundWindow(window_);
      const auto command = TrackPopupMenu(menu, TPM_RETURNCMD | TPM_RIGHTBUTTON,
                                           point.x, point.y, 0, window_, nullptr);
      DestroyMenu(menu);
      if (command == 1) { ShowWindow(window_, SW_RESTORE); SetForegroundWindow(window_); }
      if (command == 2) channel_->InvokeMethod("exitRequested", nullptr);
      if (command == 3) channel_->InvokeMethod("toggleConnection", nullptr);
    }
    return 0;
  }
  return std::nullopt;
}

void BlueGrassPlatform::HandleCall(const flutter::MethodCall<EncodableValue>& call,
    std::unique_ptr<flutter::MethodResult<EncodableValue>> result) {
  try {
    const auto& method = call.method_name();
    if (control_http_ && control_http_->Handles(method)) {
      control_http_->HandleCall(call, std::move(result));
      return;
    }
    if (method == "submitExpiryNotification") {
      result->Success(EncodableValue(notifications_ && notifications_->Submit(
          Wide(Argument(call.arguments(), "id")),
          Wide(Argument(call.arguments(), "title")),
          Wide(Argument(call.arguments(), "body")))));
    } else if (method == "cancelExpiryNotification") {
      if (!notifications_) throw std::runtime_error("notification_backend_unavailable");
      notifications_->Cancel(Wide(Argument(call.arguments(), "id")));
      result->Success();
    } else if (method == "takeExpiryNotificationActivation") {
      const auto id = notifications_ ? notifications_->TakeActivation() : std::nullopt;
      if (id) result->Success(EncodableValue(Narrow(*id))); else result->Success();
    } else if (method == "readRecord") {
      const auto value = ReadRecord(RecordPath(Argument(call.arguments(), "key")));
      if (value) result->Success(EncodableValue(*value)); else result->Success();
    } else if (method == "writeRecord") {
      WriteRecord(RecordPath(Argument(call.arguments(), "key")), Argument(call.arguments(), "value"));
      result->Success();
    } else if (method == "deleteRecord") {
      std::filesystem::remove(RecordPath(Argument(call.arguments(), "key")));
      result->Success();
    } else if (method == "getAutostart") {
      const bluegrass::RunKey run(false);
      const auto value = run.key ? bluegrass::ReadRunValue(run.key, bluegrass::kRunName) : std::nullopt;
      result->Success(EncodableValue(value && *value == bluegrass::AutostartCommand()));
    } else if (method == "setAutostart") {
      const auto* map = call.arguments() ? std::get_if<EncodableMap>(call.arguments()) : nullptr;
      if (!map) throw std::runtime_error("invalid_argument");
      const auto found = map->find(EncodableValue("enabled"));
      if (found == map->end() || !std::holds_alternative<bool>(found->second)) {
        throw std::runtime_error("invalid_argument");
      }
      const bluegrass::RunKey run(true);
      bluegrass::SetOwnedAutostart(run.key, bluegrass::kRunName, bluegrass::AutostartCommand(),
                                 std::get<bool>(found->second));
      result->Success();
    } else if (method == "openUrl") {
      const auto url = Argument(call.arguments(), "url");
      if (url.rfind("https://", 0) != 0 || url.find_first_of("\r\n") != std::string::npos) {
        throw std::runtime_error("invalid_url");
      }
      if (reinterpret_cast<INT_PTR>(ShellExecuteW(window_, L"open", Wide(url).c_str(),
                                                   nullptr, nullptr, SW_SHOWNORMAL)) <= 32) {
        throw std::runtime_error("open_url_failed");
      }
      result->Success();
    } else if (method == "setTrayState") {
      const auto label = Wide("BlueGrass · " + Argument(call.arguments(), "label"));
      wcsncpy_s(tray_.szTip, label.c_str(), _TRUNCATE);
      tray_.uFlags = NIF_TIP;
      if (tray_available_) Shell_NotifyIconW(NIM_MODIFY, &tray_);
      result->Success();
    } else if (method == "setWindowChrome") {
      chrome_ = WindowChrome{
          ColorFromArgb(IntegerArgument(call.arguments(), "caption")),
          ColorFromArgb(IntegerArgument(call.arguments(), "text")),
          BoolArgument(call.arguments(), "dark")};
      ApplyChrome();
      result->Success();
    } else if (method == "exit") {
      result->Success();
      PostMessage(window_, kExitMessage, 0, 0);
    } else {
      result->NotImplemented();
    }
  } catch (...) {
    result->Error("platform_operation_failed", "Windows 操作失败，请重试。");
  }
}

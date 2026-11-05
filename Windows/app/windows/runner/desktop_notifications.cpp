#include "desktop_notifications.h"
#include "portable_paths.h"

#include <NotificationActivationCallback.h>
#include <bcrypt.h>
#include <propkey.h>
#include <propvarutil.h>
#include <shlobj.h>
#include <wrl.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.UI.Notifications.h>
#include <algorithm>
#include <array>
#include <chrono>
#include <deque>
#include <filesystem>
#include <mutex>
#include <stdexcept>

namespace bluegrass {
namespace {
using namespace Microsoft::WRL;
using namespace winrt::Windows::Data::Xml::Dom;
using namespace winrt::Windows::UI::Notifications;

bool ValidText(const std::wstring& text, size_t limit, bool nonempty = true) {
  if ((nonempty && text.empty()) || text.size() > limit) return false;
  for (const auto value : text) {
    if (value < L' ' && value != L'\t' && value != L'\n' && value != L'\r') return false;
  }
  return true;
}

std::wstring Executable() {
  std::wstring buffer(32768, L'\0');
  const auto size = GetModuleFileNameW(nullptr, buffer.data(), static_cast<DWORD>(buffer.size()));
  if (size == 0 || size >= buffer.size()) throw std::runtime_error("executable_unavailable");
  buffer.resize(size);
  return buffer;
}

std::wstring ClsidString(const CLSID& clsid) {
  wchar_t buffer[40]{};
  if (!StringFromGUID2(clsid, buffer, 40)) throw std::runtime_error("invalid_clsid");
  return buffer;
}

class RegistryKey {
 public:
  HKEY key = nullptr;
  RegistryKey(const std::wstring& path, bool create) {
    const auto status = create
        ? RegCreateKeyExW(HKEY_CURRENT_USER, path.c_str(), 0, nullptr, 0,
                          KEY_READ | KEY_WRITE, nullptr, &key, nullptr)
        : RegOpenKeyExW(HKEY_CURRENT_USER, path.c_str(), 0, KEY_READ, &key);
    if (status != ERROR_SUCCESS && status != ERROR_FILE_NOT_FOUND) {
      throw std::runtime_error("registration_unavailable");
    }
  }
  ~RegistryKey() { if (key) RegCloseKey(key); }
  RegistryKey(const RegistryKey&) = delete;
  RegistryKey& operator=(const RegistryKey&) = delete;
  std::optional<std::wstring> Read(const wchar_t* name = nullptr) const {
    if (!key) return std::nullopt;
    DWORD type = 0, size = 0;
    const auto status = RegQueryValueExW(key, name, nullptr, &type, nullptr, &size);
    if (status == ERROR_FILE_NOT_FOUND) return std::nullopt;
    if (status != ERROR_SUCCESS || type != REG_SZ || size < sizeof(wchar_t) ||
        size > 65536 || size % sizeof(wchar_t) != 0) {
      throw std::runtime_error("invalid_registration");
    }
    std::wstring value(size / sizeof(wchar_t), L'\0');
    if (RegQueryValueExW(key, name, nullptr, &type, reinterpret_cast<BYTE*>(value.data()), &size) != ERROR_SUCCESS || value.back() != L'\0') {
      throw std::runtime_error("invalid_registration");
    }
    value.pop_back();
    if (value.find(L'\0') != std::wstring::npos) throw std::runtime_error("invalid_registration");
    return value;
  }
  void Write(const wchar_t* name, const std::wstring& value) {
    if (!key || RegSetValueExW(key, name, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(value.c_str()),
        static_cast<DWORD>((value.size() + 1) * sizeof(wchar_t))) != ERROR_SUCCESS) {
      throw std::runtime_error("registration_write_failed");
    }
  }
};

struct ActivationState {
  std::mutex mutex;
  std::deque<std::wstring> pending;
  std::wstring app_id;
  HWND window = nullptr;
  UINT message = 0;
  bool closed = false;
};

class Activator final : public RuntimeClass<RuntimeClassFlags<ClassicCom>,
    INotificationActivationCallback, FtmBase> {
 public:
  explicit Activator(std::shared_ptr<ActivationState> state) : state_(std::move(state)) {}
  IFACEMETHODIMP Activate(LPCWSTR app_id, LPCWSTR arguments,
                         const NOTIFICATION_USER_INPUT_DATA*, ULONG) override {
    try {
      if (!app_id || wcsnlen_s(app_id, 129) > 128 || !arguments || wcsnlen_s(arguments, 8193) > 8192 ||
          state_->app_id != app_id || !ValidText(arguments, 8192)) return E_INVALIDARG;
      std::lock_guard<std::mutex> lock(state_->mutex);
      if (state_->closed) return RO_E_CLOSED;
      if (state_->pending.size() == 16) state_->pending.pop_front();
      state_->pending.emplace_back(arguments);
      if (state_->window) PostMessageW(state_->window, state_->message, 0, 0);
      return S_OK;
    } catch (...) { return E_FAIL; }
  }
 private:
  std::shared_ptr<ActivationState> state_;
};

class ActivatorFactory final : public RuntimeClass<RuntimeClassFlags<ClassicCom>, IClassFactory> {
 public:
  explicit ActivatorFactory(std::shared_ptr<ActivationState> state) : state_(std::move(state)) {}
  IFACEMETHODIMP CreateInstance(IUnknown* outer, REFIID iid, void** result) override {
    if (!result) return E_POINTER;
    *result = nullptr;
    if (outer) return CLASS_E_NOAGGREGATION;
    auto activator = Make<Activator>(state_);
    return activator ? activator.CopyTo(iid, result) : E_OUTOFMEMORY;
  }
  IFACEMETHODIMP LockServer(BOOL) override { return S_OK; }
 private:
  std::shared_ptr<ActivationState> state_;
};

ComPtr<IShellLinkW> LoadShortcut(const std::filesystem::path& path, DWORD mode = STGM_READ) {
  ComPtr<IShellLinkW> link;
  winrt::check_hresult(CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER,
                                        IID_PPV_ARGS(&link)));
  if (std::filesystem::exists(path)) {
    ComPtr<IPersistFile> file;
    winrt::check_hresult(link.As(&file));
    winrt::check_hresult(file->Load(path.c_str(), mode));
  }
  return link;
}

bool ShortcutOwned(IShellLinkW* link, const std::wstring& executable,
                   const ToastIdentity& identity, bool require_properties) {
  std::array<wchar_t, 32768> target{}, args{};
  if (FAILED(link->GetPath(target.data(), static_cast<int>(target.size()), nullptr, SLGP_RAWPATH)) ||
      FAILED(link->GetArguments(args.data(), static_cast<int>(args.size()))) ||
      _wcsicmp(target.data(), executable.c_str()) != 0 || args[0] != L'\0') return false;
  ComPtr<IPropertyStore> properties;
  if (FAILED(link->QueryInterface(IID_PPV_ARGS(&properties)))) return false;
  PROPVARIANT app_id{}, activator{};
  PropVariantInit(&app_id);
  PropVariantInit(&activator);
  const auto read_app = properties->GetValue(PKEY_AppUserModel_ID, &app_id);
  const auto read_clsid = properties->GetValue(PKEY_AppUserModel_ToastActivatorCLSID, &activator);
  const bool app_matches = SUCCEEDED(read_app) &&
      ((app_id.vt == VT_EMPTY && !require_properties) ||
       (app_id.vt == VT_LPWSTR && app_id.pwszVal && identity.app_id == app_id.pwszVal));
  const bool clsid_matches = SUCCEEDED(read_clsid) &&
      ((activator.vt == VT_EMPTY && !require_properties) ||
       (activator.vt == VT_CLSID && activator.puuid && IsEqualCLSID(*activator.puuid, identity.activator)));
  PropVariantClear(&app_id);
  PropVariantClear(&activator);
  return app_matches && clsid_matches;
}

bool MissingPreviousExecutable(const std::wstring& previous, const std::wstring& current) {
  const std::filesystem::path old_path(previous), current_path(current);
  if (!old_path.is_absolute() ||
      _wcsicmp(old_path.filename().c_str(), current_path.filename().c_str()) != 0) return false;
  std::error_code error;
  const bool exists = std::filesystem::exists(old_path, error);
  return !exists && !error;
}

bool RetiredActivationCommand(const std::wstring& command, const std::wstring& current) {
  const std::wstring suffix = L"\" --toast-activated";
  if (command.size() <= suffix.size() + 1 || command.front() != L'\"' ||
      command.compare(command.size() - suffix.size(), suffix.size(), suffix) != 0) return false;
  const auto previous = command.substr(1, command.size() - suffix.size() - 1);
  return previous.find(L'\"') == std::wstring::npos && MissingPreviousExecutable(previous, current);
}

bool RetiredOwnedShortcut(IShellLinkW* link, const std::wstring& current,
                          const ToastIdentity& identity) {
  std::array<wchar_t, 32768> target{};
  return SUCCEEDED(link->GetPath(target.data(), static_cast<int>(target.size()), nullptr, SLGP_RAWPATH)) &&
      MissingPreviousExecutable(target.data(), current) && ShortcutOwned(link, target.data(), identity, true);
}

bool ShortcutIconMatches(IShellLinkW* link, const std::wstring& executable) {
  std::array<wchar_t, 32768> icon{};
  int index = -1;
  return SUCCEEDED(link->GetIconLocation(icon.data(), static_cast<int>(icon.size()), &index)) &&
      index == 0 && _wcsicmp(icon.data(), executable.c_str()) == 0;
}
}  // namespace

std::pair<std::wstring, std::wstring> ToastAddress(const std::wstring& id) {
  if (!ValidText(id, 8192)) throw std::runtime_error("invalid_notification_id");
  const auto bytes = winrt::to_string(id);
  BCRYPT_ALG_HANDLE algorithm = nullptr;
  if (BCryptOpenAlgorithmProvider(&algorithm, BCRYPT_SHA256_ALGORITHM, nullptr, 0) < 0) {
    throw std::runtime_error("notification_hash_failed");
  }
  std::array<UCHAR, 32> hash{};
  const auto status = BCryptHash(algorithm, nullptr, 0,
      reinterpret_cast<PUCHAR>(const_cast<char*>(bytes.data())),
      static_cast<ULONG>(bytes.size()), hash.data(), static_cast<ULONG>(hash.size()));
  BCryptCloseAlgorithmProvider(algorithm, 0);
  if (status < 0) throw std::runtime_error("notification_hash_failed");
  const wchar_t digits[] = L"0123456789abcdef";
  std::wstring value;
  for (size_t i = 0; i < 16; ++i) {
    value += digits[hash[i] >> 4];
    value += digits[hash[i] & 15];
  }
  return {value.substr(0, 16), value.substr(16, 16)};
}

std::wstring ToastXml(const std::wstring& id, const std::wstring& title, const std::wstring& body) {
  if (!ValidText(id, 8192) || !ValidText(title, 256) || !ValidText(body, 2048)) {
    throw std::runtime_error("invalid_notification");
  }
  XmlDocument xml;
  xml.LoadXml(L"<toast><visual><binding template=\"ToastGeneric\"><text/><text/></binding></visual><audio silent=\"true\"/></toast>");
  xml.DocumentElement().SetAttribute(L"launch", id);
  xml.DocumentElement().SetAttribute(L"activationType", L"foreground");
  const auto text = xml.GetElementsByTagName(L"text");
  text.Item(0).AppendChild(xml.CreateTextNode(title));
  text.Item(1).AppendChild(xml.CreateTextNode(body));
  return std::wstring(xml.GetXml());
}

class DesktopNotifications::Impl {
 public:
  ToastIdentity identity;
  std::wstring executable, command, clsid_path, app_path;
  std::filesystem::path shortcut;
  std::shared_ptr<ActivationState> state = std::make_shared<ActivationState>();
  DWORD registration = 0;
  bool ready = false;

  Impl(HWND window, UINT message, ToastIdentity value) : identity(std::move(value)) {
    if (identity.app_id.empty() || identity.app_id.size() > 128 ||
        identity.app_id.find_first_not_of(L"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-") != std::wstring::npos ||
        identity.shortcut_name.empty() ||
        identity.shortcut_name.find_first_of(L"/\\:") != std::wstring::npos ||
        !std::filesystem::path(identity.shortcut_name).has_extension()) {
      throw std::runtime_error("invalid_notification_registration");
    }
    const auto actual_executable = Executable();
    executable = PublicExecutable(actual_executable);
    command = L"\"" + actual_executable + L"\" --toast-activated";
    clsid_path = L"Software\\Classes\\CLSID\\" + ClsidString(identity.activator);
    app_path = L"Software\\Classes\\AppUserModelId\\" + identity.app_id;
    PWSTR programs = nullptr;
    winrt::check_hresult(SHGetKnownFolderPath(FOLDERID_Programs, KF_FLAG_CREATE, nullptr, &programs));
    shortcut = std::filesystem::path(programs) / identity.shortcut_name;
    CoTaskMemFree(programs);
    state->app_id = identity.app_id;
    state->window = window;
    state->message = message;
    winrt::check_hresult(SetCurrentProcessExplicitAppUserModelID(identity.app_id.c_str()));
    const auto factory = Make<ActivatorFactory>(state);
    if (!factory) throw std::runtime_error("notification_activation_unavailable");
    winrt::check_hresult(CoRegisterClassObject(identity.activator, factory.Get(), CLSCTX_LOCAL_SERVER,
                                              REGCLS_MULTIPLEUSE, &registration));
  }
  ~Impl() {
    {
      std::lock_guard<std::mutex> lock(state->mutex);
      state->closed = true;
      state->window = nullptr;
    }
    if (registration) CoRevokeClassObject(registration);
  }
};

DesktopNotifications::DesktopNotifications(HWND window, UINT message, ToastIdentity identity)
    : impl_(std::make_unique<Impl>(window, message, std::move(identity))) {}
DesktopNotifications::~DesktopNotifications() = default;

bool DesktopNotifications::HasOwnedRegistration() const {
  const RegistryKey server(impl_->clsid_path + L"\\LocalServer32", false);
  const RegistryKey app(impl_->app_path, false);
  if (server.Read() != impl_->command ||
      app.Read(L"CustomActivator") != ClsidString(impl_->identity.activator) ||
      !std::filesystem::exists(impl_->shortcut)) return false;
  const auto link = LoadShortcut(impl_->shortcut);
  return ShortcutOwned(link.Get(), impl_->executable, impl_->identity, true) &&
      ShortcutIconMatches(link.Get(), impl_->executable);
}

bool DesktopNotifications::EnsureRegistration() {
  if (HasOwnedRegistration()) return true;
  // Preserve live installations and foreign identities. A matching retired
  // executable may be rebound after the portable bundle has moved directories.
  const RegistryKey existing_server(impl_->clsid_path + L"\\LocalServer32", false);
  const auto existing_command = existing_server.Read();
  // An upgrade from the flat ZIP keeps its public launcher at the same path.
  // Rebind only that exact former activator to the actual COM server, so cold
  // notification activation never depends on the launcher's short lifetime.
  const auto public_command = L"\"" + impl_->executable + L"\" --toast-activated";
  if (existing_command && _wcsicmp(existing_command->c_str(), impl_->command.c_str()) != 0 &&
      _wcsicmp(existing_command->c_str(), public_command.c_str()) != 0 &&
      !RetiredActivationCommand(*existing_command, impl_->executable)) return false;
  const RegistryKey existing_app(impl_->app_path, false);
  const auto existing_activator = existing_app.Read(L"CustomActivator");
  if (existing_activator && *existing_activator != ClsidString(impl_->identity.activator)) return false;
  auto link = LoadShortcut(impl_->shortcut, STGM_READWRITE);
  if (std::filesystem::exists(impl_->shortcut) &&
      !ShortcutOwned(link.Get(), impl_->executable, impl_->identity, false) &&
      !RetiredOwnedShortcut(link.Get(), impl_->executable, impl_->identity)) return false;

  RegistryKey server(impl_->clsid_path + L"\\LocalServer32", true);
  server.Write(nullptr, impl_->command);
  RegistryKey app(impl_->app_path, true);
  app.Write(L"CustomActivator", ClsidString(impl_->identity.activator));
  app.Write(L"DisplayName", L"BlueGrass");
  winrt::check_hresult(link->SetPath(impl_->executable.c_str()));
  winrt::check_hresult(link->SetArguments(L""));
  winrt::check_hresult(link->SetWorkingDirectory(std::filesystem::path(impl_->executable).parent_path().c_str()));
  winrt::check_hresult(link->SetDescription(L"BlueGrass"));
  winrt::check_hresult(link->SetIconLocation(impl_->executable.c_str(), 0));
  ComPtr<IPropertyStore> properties;
  winrt::check_hresult(link.As(&properties));
  PROPVARIANT app_id{}, activator{};
  winrt::check_hresult(InitPropVariantFromString(impl_->identity.app_id.c_str(), &app_id));
  HRESULT status = properties->SetValue(PKEY_AppUserModel_ID, app_id);
  PropVariantClear(&app_id);
  winrt::check_hresult(status);
  winrt::check_hresult(InitPropVariantFromCLSID(impl_->identity.activator, &activator));
  status = properties->SetValue(PKEY_AppUserModel_ToastActivatorCLSID, activator);
  PropVariantClear(&activator);
  winrt::check_hresult(status);
  winrt::check_hresult(properties->Commit());
  ComPtr<IPersistFile> file;
  winrt::check_hresult(link.As(&file));
  winrt::check_hresult(file->Save(impl_->shortcut.c_str(), TRUE));
  SHChangeNotify(SHCNE_UPDATEITEM, SHCNF_PATHW | SHCNF_FLUSH, impl_->shortcut.c_str(), nullptr);
  impl_->ready = HasOwnedRegistration();
  return impl_->ready;
}

bool DesktopNotifications::Submit(const std::wstring& id, const std::wstring& title,
                                  const std::wstring& body, bool suppress_popup, int lifetime_seconds) {
  try {
    if (!EnsureRegistration()) return false;
    const auto notifier = ToastNotificationManager::CreateToastNotifier(impl_->identity.app_id);
    try {
      if (notifier.Setting() != NotificationSetting::Enabled) return false;
    } catch (const winrt::hresult_error& error) {
      // An unpackaged app has no per-app settings profile until its first
      // notification. Only this precise absence allows an initial attempt;
      // all explicit disabled states and other lookup failures remain denied.
      if (error.code() != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) return false;
    }
    const auto address = ToastAddress(id);
    bool replacement = false;
    try {
      for (const auto& item : ToastNotificationManager::History().GetHistory(impl_->identity.app_id)) {
        if (item.Tag() == address.first && item.Group() == address.second) replacement = true;
      }
    } catch (const winrt::hresult_error& error) {
      if (error.code() != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) throw;
    }
    XmlDocument xml;
    xml.LoadXml(ToastXml(id, title, body));
    ToastNotification toast(xml);
    toast.Tag(address.first);
    toast.Group(address.second);
    toast.SuppressPopup(suppress_popup || replacement);
    const auto seconds = std::clamp(lifetime_seconds, 1, 7 * 24 * 60 * 60);
    toast.ExpirationTime(winrt::clock::now() + std::chrono::seconds(seconds));
    notifier.Show(toast);
    return true;
  } catch (...) {
    // Neither WinRT error text nor payloads cross the UI/log boundary.
    return false;
  }
}

void DesktopNotifications::Cancel(const std::wstring& id) {
  const auto address = ToastAddress(id);
  // Removing a missing entry is an idempotent Windows operation. Do not create
  // persistent registration merely because a previous account is being cleared.
  try {
    ToastNotificationManager::History().Remove(address.first, address.second, impl_->identity.app_id);
  } catch (const winrt::hresult_error& error) {
    if (error.code() != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) throw;
  }
}

std::optional<std::wstring> DesktopNotifications::TakeActivation() {
  std::lock_guard<std::mutex> lock(impl_->state->mutex);
  if (impl_->state->pending.empty()) return std::nullopt;
  auto id = std::move(impl_->state->pending.front());
  impl_->state->pending.pop_front();
  return id;
}

bool DesktopNotifications::RemoveOwnedRegistration() {
  if (impl_->identity.app_id.rfind(L"com.bluegrass.verification.", 0) != 0) return false;
  if (std::filesystem::exists(impl_->shortcut)) {
    if (!ShortcutOwned(LoadShortcut(impl_->shortcut).Get(), impl_->executable, impl_->identity, true)) return false;
    std::filesystem::remove(impl_->shortcut);
  }
  {
    const RegistryKey server(impl_->clsid_path + L"\\LocalServer32", false);
    if (server.Read() && server.Read() != impl_->command) return false;
    const RegistryKey app(impl_->app_path, false);
    if (app.Read(L"CustomActivator") && app.Read(L"CustomActivator") != ClsidString(impl_->identity.activator)) return false;
  }
  const auto remove = [](const std::wstring& path) {
    const auto status = RegDeleteKeyW(HKEY_CURRENT_USER, path.c_str());
    return status == ERROR_SUCCESS || status == ERROR_FILE_NOT_FOUND;
  };
  const bool removed_server = remove(impl_->clsid_path + L"\\LocalServer32");
  const bool removed_class = remove(impl_->clsid_path);
  const bool removed_app = remove(impl_->app_path);
  const bool removed_settings = remove(
      L"Software\\Microsoft\\Windows\\CurrentVersion\\Notifications\\Settings\\" + impl_->identity.app_id);
  impl_->ready = false;
  return removed_server && removed_class && removed_app && removed_settings;
}
}  // namespace bluegrass

import 'platform_store.dart';
import 'desktop_platform.dart';
import 'preferences.dart';

abstract class AutostartRegistration {
  Future<bool> read();
  Future<void> setEnabled(bool enabled);
}

class WindowsAutostartRegistration extends AutostartRegistration {
  @override
  Future<bool> read() async =>
      await platform.invokeMethod<bool>('getAutostart') ?? false;

  @override
  Future<void> setEnabled(bool enabled) =>
      platform.invokeMethod<void>('setAutostart', {'enabled': enabled});
}

/// User-session settings only. Native protection and connection recovery remain
/// independent of these switches; enabling either never starts a connection.
class UserLifecycle {
  UserLifecycle({
    required this.preferences,
    required this.save,
    required this.registration,
  });
  final Preferences Function() preferences;
  final Future<bool> Function(Preferences) save;
  final AutostartRegistration registration;
  bool busy = false;
  bool _promptClaimed = false;
  String? error;

  Future<bool> setStartWithWindows(bool enabled) async {
    if (busy) return false;
    busy = true;
    error = null;
    bool? previous;
    var changed = false;
    try {
      previous = await registration.read();
      await registration.setEnabled(enabled);
      changed = true;
      if (!await save(preferences().copyWith(startWithWindows: enabled))) {
        throw StateError('preference_write_failed');
      }
      return true;
    } catch (_) {
      error = 'STARTUP-001 · 自启设置未保存，请重试；其他程序的启动项不会被修改。';
      if (changed && previous != null) {
        try {
          await registration.setEnabled(previous);
        } catch (_) {
          error = DesktopPlatform.current.isMac
              ? 'STARTUP-002 · 无法回退登录项登记，请在 macOS 系统设置的登录项中检查 BlueGrass。'
              : 'STARTUP-002 · 无法回退自启登记，请在 Windows 启动应用中检查 BlueGrass。';
        }
      }
      return false;
    } finally {
      busy = false;
    }
  }

  /// Persist before presenting so dismissal, restart, and concurrent callbacks
  /// cannot repeat a prompt. Only the explicit manual-connect path calls this.
  Future<bool> claimStartupPrompt({
    required bool manual,
    required bool connected,
  }) async {
    if (!manual ||
        !connected ||
        _promptClaimed ||
        preferences().startWithWindows ||
        preferences().startupPromptShown) {
      return false;
    }
    _promptClaimed = true;
    return save(preferences().copyWith(startupPromptShown: true));
  }
}

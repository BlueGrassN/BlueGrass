import 'package:flutter/services.dart';

import 'expiry_reminder_coordinator.dart';
import 'platform_store.dart';

/// Native WinRT acceptance only, not proof that Windows displayed a banner or
/// that the user read it. Permission/policy denial leaves the in-app list intact.
class WindowsReminderNotifications extends ReminderNotifications {
  @override
  Future<bool> submit(ReminderNotice notice) async {
    try {
      return await platform.invokeMethod<bool>('submitExpiryNotification', {
            'id': notice.id,
            'title': notice.title,
            'body': notice.body,
          }) ==
          true;
    } on PlatformException {
      return false;
    } on MissingPluginException {
      return false;
    }
  }

  @override
  Future<void> cancel(String id) =>
      platform.invokeMethod<void>('cancelExpiryNotification', {'id': id});

  /// The native queue retains a cold-start click until Flutter installs its
  /// handler. The UI then waits for the current account's synchronized ledger.
  Future<String?> takeActivation() =>
      platform.invokeMethod<String>('takeExpiryNotificationActivation');
}

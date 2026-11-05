import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/user_lifecycle.dart';
import 'package:flutter_test/flutter_test.dart';

class Registration extends AutostartRegistration {
  bool enabled = false;
  bool fail = false;
  @override
  Future<bool> read() async => enabled;
  @override
  Future<void> setEnabled(bool value) async {
    if (fail) throw StateError('denied');
    enabled = value;
  }
}

void main() {
  test(
    'autorun enables independently and rolls back a failed preference write',
    () async {
      var prefs = const Preferences();
      final registration = Registration();
      var saveSucceeds = true;
      final lifecycle = UserLifecycle(
        preferences: () => prefs,
        save: (value) async {
          if (saveSucceeds) prefs = value;
          return saveSucceeds;
        },
        registration: registration,
      );
      expect(await lifecycle.setStartWithWindows(true), isTrue);
      expect(registration.enabled, isTrue);
      expect(prefs.startWithWindows, isTrue);
      expect(prefs.autoConnect, isFalse);
      saveSucceeds = false;
      expect(await lifecycle.setStartWithWindows(false), isFalse);
      expect(registration.enabled, isTrue);
      expect(prefs.startWithWindows, isTrue);
      expect(lifecycle.error, isNotNull);
    },
  );

  test(
    'a rejected registry operation never persists enabled startup',
    () async {
      var prefs = const Preferences();
      final lifecycle = UserLifecycle(
        preferences: () => prefs,
        save: (value) async {
          prefs = value;
          return true;
        },
        registration: Registration()..fail = true,
      );
      expect(await lifecycle.setStartWithWindows(true), isFalse);
      expect(prefs.startWithWindows, isFalse);
    },
  );

  test(
    'startup prompt needs verified manual success and is consumed once',
    () async {
      var prefs = const Preferences();
      UserLifecycle create() => UserLifecycle(
        preferences: () => prefs,
        save: (value) async {
          prefs = value;
          return true;
        },
        registration: Registration(),
      );
      final lifecycle = create();
      expect(
        await lifecycle.claimStartupPrompt(manual: false, connected: true),
        isFalse,
      );
      expect(
        await lifecycle.claimStartupPrompt(manual: true, connected: false),
        isFalse,
      );
      expect(prefs.startupPromptShown, isFalse);
      final claims = await Future.wait([
        lifecycle.claimStartupPrompt(manual: true, connected: true),
        lifecycle.claimStartupPrompt(manual: true, connected: true),
      ]);
      expect(claims.where((v) => v), hasLength(1));
      expect(prefs.startupPromptShown, isTrue);
      expect(
        await create().claimStartupPrompt(manual: true, connected: true),
        isFalse,
      );
      expect(prefs.autoConnect, isFalse);
    },
  );

  test(
    'actively enabled autorun suppresses prompt without enabling auto-connect',
    () async {
      final prefs = const Preferences(startWithWindows: true);
      final lifecycle = UserLifecycle(
        preferences: () => prefs,
        save: (_) async => throw StateError('must not write'),
        registration: Registration(),
      );
      expect(
        await lifecycle.claimStartupPrompt(manual: true, connected: true),
        isFalse,
      );
      expect(prefs.autoConnect, isFalse);
    },
  );
}

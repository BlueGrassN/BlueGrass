import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('startup opt-ins and dismissed prompts survive a theme change', () {
    final stored = Preferences.fromJson({
      'startWithWindows': true,
      'autoConnect': true,
      'startupPromptShown': true,
      'socksWarningShown': true,
    }).copyWith(theme: ThemeMode.dark).toJson();
    for (final key in [
      'startWithWindows',
      'autoConnect',
      'startupPromptShown',
      'socksWarningShown',
    ]) {
      expect(stored[key], isTrue, reason: '$key must persist independently');
    }
  });

  test('old or malformed settings never opt into automatic actions', () {
    for (final value in [null, 'true', 1, [], {}]) {
      final stored = Preferences.fromJson({
        'startWithWindows': value,
        'autoConnect': value,
        'startupPromptShown': value,
      }).toJson();
      expect(stored['startWithWindows'] == true, isFalse);
      expect(stored['autoConnect'] == true, isFalse);
      expect(stored['startupPromptShown'] == true, isFalse);
    }
  });
}

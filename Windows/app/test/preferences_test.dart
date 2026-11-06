import 'dart:convert';

import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final records = <String, String>{};
  setUp(() {
    records.clear();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(platform, (call) async {
          final args = call.arguments as Map;
          if (call.method == 'readRecord') return records[args['key']];
          if (call.method == 'writeRecord') {
            records[args['key'] as String] = args['value'] as String;
          }
          if (call.method == 'deleteRecord') records.remove(args['key']);
          return null;
        });
  });

  test(
    'fresh install has system theme proxy and LAN without automatic actions',
    () async {
      final prefs = await Preferences.read();
      expect(prefs.theme, ThemeMode.system);
      expect(prefs.mode, 'system_proxy');
      expect(prefs.toJson()['routingMode'], 'bypass_cn');
      expect(prefs.allowLan, isTrue);
      expect(prefs.toJson().containsKey('autoConnect'), isFalse);
    },
  );
  test('mode and theme survive restart', () async {
    await const Preferences(
      theme: ThemeMode.dark,
      mode: 'tun',
      allowLan: false,
    ).save();
    final restored = await Preferences.read();
    expect(restored.theme, ThemeMode.dark);
    expect(restored.mode, 'tun');
    expect(restored.allowLan, isFalse);
  });
  test('invalid preference values do not enable unexpected behavior', () {
    final prefs = Preferences.fromJson({
      'theme': 'other',
      'mode': 'bad',
      'allowLan': 'false',
    });
    expect(prefs.theme, ThemeMode.system);
    expect(prefs.mode, 'system_proxy');
    expect(prefs.allowLan, isTrue);
  });
  test(
    'legacy preferences keep global routing across save and restart',
    () async {
      records['preferences'] = jsonEncode({
        'mode': 'tun',
        'allowLan': false,
        'theme': 'dark',
        'autoConnect': true,
      });
      final migrated = await Preferences.read();
      expect(migrated.toJson()['routingMode'], 'global');
      expect(migrated.mode, 'tun');
      expect(migrated.allowLan, isFalse);
      expect(migrated.autoConnect, isTrue);
      await migrated.copyWith(theme: ThemeMode.light).save();
      expect((await Preferences.read()).toJson()['routingMode'], 'global');
    },
  );
  for (final routingMode in ['global', 'bypass_cn']) {
    test('$routingMode persists independently of takeover mode', () async {
      final prefs = Preferences.fromJson({'routingMode': routingMode});
      await prefs.copyWith(mode: 'tun').save();
      final restored = await Preferences.read();
      expect(restored.toJson()['routingMode'], routingMode);
      expect(restored.mode, 'tun');
      await restored.copyWith(mode: 'system_proxy').save();
      expect((await Preferences.read()).toJson()['routingMode'], routingMode);
    });
  }
  test('invalid stored routing modes fall back to global', () async {
    for (final invalid in <Object?>[
      null,
      '',
      'direct',
      'BYPASS_CN',
      1,
      true,
      [],
      {},
    ]) {
      records['preferences'] = jsonEncode({'routingMode': invalid});
      final prefs = await Preferences.read();
      expect(prefs.toJson()['routingMode'], 'global', reason: '$invalid');
      await prefs.save();
      expect(jsonDecode(records['preferences']!)['routingMode'], 'global');
    }
  });
  test('device id persists and is a nonzero v4 UUID', () async {
    final first = await deviceId();
    expect(await deviceId(), first);
    expect(
      first,
      matches(
        RegExp(
          r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
        ),
      ),
    );
  });
  test('session store replaces complete snapshot and clears it', () async {
    final store = WindowsSessionStore('test_session');
    await store.replace({'generation': 1, 'old': 'fake-token'});
    await store.replace({'generation': 2, 'current': 'new-fake-token'});
    expect(await store.read(), {'generation': 2, 'current': 'new-fake-token'});
    expect(jsonDecode(records['test_session']!).containsKey('old'), isFalse);
    await store.clear();
    expect(await store.read(), isNull);
  });
}

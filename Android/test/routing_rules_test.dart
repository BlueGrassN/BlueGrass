import 'dart:async';
import 'dart:convert';

import 'package:client/app_controller.dart';
import 'package:client/platform/routing_rules_client.dart';
import 'package:client/routing_rules.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

Map<String, Object> payload([Map<String, Object> overrides = const {}]) => {
  'version': '202610232206',
  'source': 'bundled',
  'checkedAt': '',
  'updatedAt': '',
  'updating': false,
  'lastError': '',
  ...overrides,
};

RoutingRulesStatus status([Map<String, Object> overrides = const {}]) =>
    RoutingRulesStatus.fromJson(jsonEncode(payload(overrides)));

class FakeRulesClient implements RoutingRulesClient {
  Future<RoutingRulesStatus> Function()? onStatus, onUpdate;
  int reads = 0, updates = 0;

  @override
  Future<RoutingRulesStatus> status() async {
    reads++;
    return onStatus?.call() ??
        RoutingRulesStatus.fromJson(jsonEncode(payload()));
  }

  @override
  Future<RoutingRulesStatus> update() async {
    updates++;
    return onUpdate?.call() ??
        RoutingRulesStatus.fromJson(jsonEncode(payload()));
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'absent optional client settles without touching a platform channel',
    () async {
      final model = RoutingRulesModel();
      addTearDown(model.dispose);
      await model.refresh();
      expect(model.available, isFalse);
      expect(model.loading, isFalse);
      expect(model.error, '暂时无法读取规则状态，请稍后重试。');
      await model.update();
      expect(model.updating, isFalse);
      expect(model.error, '规则更新失败，请稍后重试。');
    },
  );

  test('parses UTC timestamps including fractional native RFC3339', () {
    final parsed = status({
      'checkedAt': '2026-10-24T01:02:03.123456789Z',
      'updatedAt': '2026-10-24T01:02:03+00:00',
      'source': 'downloaded',
    });
    expect(parsed.checkedAt, DateTime.utc(2026, 10, 24, 1, 2, 3, 123, 456));
    expect(parsed.updatedAt, DateTime.utc(2026, 10, 24, 1, 2, 3));
    expect(status().checkedAt, isNull);
    expect(status().updatedAt, isNull);
  });

  for (final bad in <String, Object>{
    'version': 'PRIVATE_NATIVE_DETAIL',
    'source': 'PRIVATE_NATIVE_DETAIL',
    'updating': 'false',
    'lastError': 3,
    'checkedAt': '2026-10-24T01:02:03',
    'updatedAt': '2026-02-31T01:02:03Z',
  }.entries) {
    test('rejects malformed ${bad.key} instead of displaying native text', () {
      expect(() => status({bad.key: bad.value}), throwsFormatException);
    });
  }

  test('unknown errors are normalized at the presentation boundary', () {
    expect(
      status({'lastError': 'PRIVATE_NATIVE_DETAIL'}).lastError,
      'UPDATE_FAILED',
    );
    expect(() => RoutingRulesStatus.fromJson('{}'), throwsFormatException);
    expect(() => RoutingRulesStatus.fromJson('[]'), throwsFormatException);
  });

  for (final code in [
    'RULES_CONFIG_FAILED',
    'RULES_UPDATE_FAILED',
    'RULES_UNAVAILABLE',
  ]) {
    test(
      'bridge $code cannot replace a valid version with an empty version',
      () async {
        expect(status({'lastError': code}).lastError, 'UPDATE_FAILED');
        final client = FakeRulesClient();
        final model = RoutingRulesModel(client: client);
        addTearDown(model.dispose);
        await model.refresh();
        client.onStatus = () async =>
            status({'version': '', 'lastError': code});
        await model.refresh();
        expect(model.status!.version, '202610232206');
        expect(model.error, '暂时无法读取规则状态，请稍后重试。');
        expect(model.available, isFalse);
        client.onUpdate = client.onStatus;
        await model.update();
        expect(model.status!.version, '202610232206');
        expect(model.error, '规则更新失败，请稍后重试。');
      },
    );
  }

  test(
    'channel uses JSON strings and no arguments for both commands',
    () async {
      const channel = MethodChannel('bluegrass/routing-rules');
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      final calls = <MethodCall>[];
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        return jsonEncode(payload({'updating': call.method == 'status'}));
      });
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      const client = MethodChannelRoutingRulesClient();
      expect((await client.status()).updating, isTrue);
      expect((await client.update()).updating, isFalse);
      expect(calls.map((c) => c.method), ['status', 'update']);
      expect(calls.map((c) => c.arguments), [null, null]);
    },
  );

  test(
    'bad or unavailable status preserves the entire last valid snapshot',
    () async {
      final client = FakeRulesClient();
      final model = RoutingRulesModel(client: client);
      addTearDown(model.dispose);
      await model.refresh();
      final previous = model.status;
      for (final error in [
        MissingPluginException('PRIVATE_NATIVE_DETAIL'),
        const FormatException('PRIVATE_NATIVE_DETAIL'),
        PlatformException(code: 'PRIVATE_NATIVE_DETAIL'),
      ]) {
        client.onStatus = () async => throw error;
        await model.refresh();
        expect(model.status, same(previous));
        expect(model.available, isFalse);
        expect(model.error, '暂时无法读取规则状态，请稍后重试。');
        expect(model.canUpdate, isTrue);
      }
      client.onStatus = null;
      await model.refresh();
      expect(model.available, isTrue);
      expect(model.error, isNull);
    },
  );

  test(
    'overlapping reads share one flight and loading blocks manual update',
    () async {
      final pending = Completer<RoutingRulesStatus>();
      final client = FakeRulesClient()..onStatus = () => pending.future;
      final model = RoutingRulesModel(client: client);
      addTearDown(model.dispose);
      final first = model.refresh();
      final second = model.refresh();
      expect(model.loading, isTrue);
      await model.update();
      expect(client.updates, 0);
      pending.complete(status());
      await Future.wait([first, second]);
      expect(client.reads, 1);
      expect(model.loading, isFalse);
      expect(model.canUpdate, isTrue);
    },
  );

  test('old polling response cannot overwrite a completed update', () async {
    final update = Completer<RoutingRulesStatus>();
    final poll = Completer<RoutingRulesStatus>();
    final client = FakeRulesClient()..onUpdate = () => update.future;
    final model = RoutingRulesModel(client: client);
    addTearDown(model.dispose);
    await model.refresh();
    final updating = model.update();
    await model.update();
    expect(client.updates, 1);
    client.onStatus = () => poll.future;
    final reading = model.refresh();
    update.complete(
      status({'version': '202610240901', 'source': 'downloaded'}),
    );
    await updating;
    poll.complete(status({'updating': true}));
    await reading;
    expect(model.status!.version, '202610240901');
    expect(model.updating, isFalse);
    expect(model.canUpdate, isTrue);
  });

  for (final update in [false, true]) {
    test(
      'disposed model ignores late ${update ? 'update' : 'status'} failure',
      () async {
        final pending = Completer<RoutingRulesStatus>();
        final client = FakeRulesClient();
        client.onStatus = () => pending.future;
        client.onUpdate = () => pending.future;
        final model = RoutingRulesModel(client: client);
        var notifications = 0;
        model.addListener(() => notifications++);
        final running = update ? model.update() : model.refresh();
        model.dispose();
        final before = notifications;
        pending.completeError(MissingPluginException('PRIVATE_NATIVE_DETAIL'));
        await running;
        expect(notifications, before);
      },
    );
  }

  test('optional metadata is not required for init login or VPN', () async {
    final client = FakeRulesClient()
      ..onStatus = () async => throw MissingPluginException();
    final vpn = FakeVpnClient();
    final controller = AppController(
      source: FakeSource(),
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
      routingRulesClient: client,
    );
    addTearDown(() async {
      controller.dispose();
      await vpn.events.close();
    });
    await controller.initialize();
    expect(client.reads, 0);
    await controller.login('demo', 'fixture');
    await controller.routingRules.refresh();
    expect(controller.authError, isNull);
    expect(controller.initialized, isTrue);
    expect(controller.canConnect, isTrue);
    await controller.connect();
    expect(vpn.requests, hasLength(1));
    await controller.disconnect();
  });
}

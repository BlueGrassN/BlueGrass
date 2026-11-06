import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

NodeRecord freshNode({
  DateTime? expiry,
  String name = 'metadata',
  String host = 'entry.example.invalid',
  int port = 1080,
  String password = 'fixture-secret',
  ProtocolConfig? protocol,
  bool forwarded = true,
}) => NodeRecord(
  key: node('1').key,
  name: name,
  customerRemark: 'updated remark',
  host: host,
  port: port,
  protocol: protocol?.type ?? 'socks5',
  username: protocol == null ? 'fixture-user' : '',
  password: protocol == null ? password : '',
  protocolConfig: protocol,
  expiresAtUtc: expiry,
  isForwarded: forwarded,
);

void main() {
  late FakeSource source;
  late FakeVpnClient vpn;
  late AppController controller;
  setUp(() {
    source = FakeSource()..customer = testCustomer;
    vpn = FakeVpnClient();
    controller = AppController(
      source: source,
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
    );
  });
  tearDown(() {
    controller.dispose();
    vpn.events.close();
  });

  Future<void> connected() async {
    await controller.initialize();
    await controller.connect();
    vpn.emit(
      snapshot(
        generation: vpn.current.generation + 1,
        state: 'connected',
        request: vpn.requests.single.requestId,
      ),
    );
  }

  for (final failure in [
    const ApiFailure('network_error'),
    const ApiFailure('timeout'),
    const ApiFailure('rate_limited', status: 429),
    const ApiFailure('server_error', status: 500),
    const ApiFailure('service_unavailable', status: 503),
    const ApiFailure('invalid_response'),
    const ApiFailure('session_expired', status: 401),
    const ApiFailure('session_revoked', status: 401),
    const ApiFailure('not_authenticated'),
  ]) {
    test(
      '${failure.code}/${failure.status} retains verified active session and expiry',
      () async {
        await connected();
        final before = controller.vpnSnapshot;
        final expiry = controller.activeResource!.expiresAtUtc;
        source.onNode = (_) async => throw failure;
        await controller.refreshVpnEligibility();
        expect(controller.vpnSnapshot, same(before));
        expect(controller.activeResource!.expiresAtUtc, expiry);
        expect(vpn.pauses, isEmpty);
        expect(vpn.updates, isEmpty);
        expect(controller.vpnError, isNotNull);
      },
    );
  }

  test(
    'API session expiration during full refresh retains dataplane and lease',
    () async {
      await connected();
      final before = controller.vpnSnapshot;
      source.onNodes = (_) async {
        source.customer = null;
        throw const ApiFailure('session_expired', status: 401);
      };
      source.onNode = (_) async => throw const ApiFailure('not_authenticated');
      await controller.refresh();
      expect(controller.vpnSnapshot, same(before));
      expect(controller.customer, testCustomer);
      expect(controller.authError, isNotNull);
      expect(vpn.pauses, isEmpty);
      expect(vpn.disconnects, 0);
    },
  );

  test('temporary malformed node is a sync error and not revocation', () async {
    await connected();
    source.onNode = (_) async => freshNode(host: '');
    await controller.refreshVpnEligibility();
    expect(controller.vpnState, VpnState.connected);
    expect(vpn.pauses, isEmpty);
    expect(vpn.updates, isEmpty);
    expect(controller.vpnError, isNotNull);
  });

  for (final failure in [
    const ApiFailure('resource_revoked'),
    const ApiFailure('resource_disabled'),
    const ApiFailure('resource_expired'),
    const ApiFailure('not_found', status: 404),
  ]) {
    test(
      'authoritative ${failure.code}/${failure.status} pauses resource',
      () async {
        await connected();
        source.onNode = (_) async => throw failure;
        await controller.refreshVpnEligibility();
        expect(controller.vpnState, VpnState.paused);
        expect(controller.vpnSnapshot!.tunActive, isTrue);
        expect(vpn.updates, isEmpty);
      },
    );
  }

  test('explicit disabled fresh node pauses without revalidation', () async {
    await connected();
    source.onNode = (_) async => freshNode(forwarded: false);
    await controller.refreshVpnEligibility();
    expect(controller.vpnState, VpnState.paused);
    expect(vpn.updates, isEmpty);
  });

  test(
    'each READY refresh binds native update and uses connect serialization',
    () async {
      await connected();
      final before = controller.vpnSnapshot!;
      await controller.refreshVpnEligibility();
      final update = vpn.updates.single;
      expect(update.keys.toSet(), {
        'runtimeId',
        'requestId',
        'accountKey',
        'resourceKey',
        'config',
      });
      expect(update['runtimeId'], before.runtimeId);
      expect(update['requestId'], before.requestId);
      expect(update['accountKey'], '1');
      expect(update['resourceKey'], node('1').key.stableId);
      expect(
        jsonDecode(update['config']!),
        jsonDecode(vpn.requests.single.config),
      );
      await controller.refreshVpnEligibility();
      expect(vpn.updates, hasLength(2));
      expect(vpn.preparations, 1);
    },
  );

  test(
    'metadata only refresh remains connected and reflects native acceptance',
    () async {
      await connected();
      source.onNode = (_) async =>
          freshNode(expiry: node('1').expiresAtUtc, name: 'renamed');
      await controller.refreshVpnEligibility();
      expect(vpn.updates, hasLength(1));
      expect(controller.activeResource!.name, 'renamed');
      final config = jsonDecode(vpn.updates.single['config']!) as Map;
      expect(config.containsKey('name'), isFalse);
      expect(config.containsKey('customerRemark'), isFalse);
      expect(controller.vpnState, VpnState.connected);
      expect(vpn.pauses, isEmpty);
    },
  );

  for (final change in ['host', 'port', 'password', 'protocol/TLS']) {
    test(
      '$change reaches native comparison and CONFIG_CHANGED is authoritative',
      () async {
        await connected();
        source.onNode = (_) async => freshNode(
          host: change == 'host'
              ? 'other.example.invalid'
              : 'entry.example.invalid',
          port: change == 'port' ? 2080 : 1080,
          password: change == 'password' ? 'changed-secret' : 'fixture-secret',
          protocol: change == 'protocol/TLS'
              ? ProtocolConfig.fromJson({
                  'version': 1,
                  'type': 'https',
                  'http': {'username': 'u', 'password': 'p'},
                  'tls': {
                    'serverName': 'tls.example.invalid',
                    'alpn': ['http/1.1'],
                  },
                })
              : null,
        );
        vpn.onUpdate = (args) async => vpn.current = snapshot(
          generation: vpn.current.generation + 1,
          state: 'paused',
          request: args['requestId'],
          errorCode: 'CONFIG_CHANGED',
        );
        await controller.refreshVpnEligibility();
        expect(vpn.updates, hasLength(1));
        expect(
          jsonDecode(vpn.updates.single['config']!),
          isNot(jsonDecode(vpn.requests.single.config)),
        );
        expect(controller.vpnState, VpnState.paused);
        expect(controller.vpnSnapshot!.tunActive, isTrue);
        expect(controller.vpnSnapshot!.errorCode, 'CONFIG_CHANGED');
        expect(vpn.pauses, isEmpty);
      },
    );
  }

  test(
    'restored native session is revalidated without a local transport cache',
    () async {
      vpn.current = snapshot(state: 'connected');
      vpn.onUpdate = (args) async => vpn.current = snapshot(
        generation: 2,
        state: 'paused',
        request: args['requestId'],
        errorCode: 'CONFIG_CHANGED',
      );
      await controller.initialize();
      expect(vpn.requests, isEmpty);
      expect(vpn.updates, hasLength(1));
      expect(controller.vpnState, VpnState.paused);
    },
  );

  test(
    'unsupported native update fails closed without changing the stored lease',
    () async {
      await connected();
      final before = controller.activeResource!.expiresAtUtc;
      source.onNode = (_) async =>
          freshNode(expiry: testNow.add(const Duration(days: 60)));
      vpn.onUpdate = (_) async => throw const VpnFailure('VPN_UNAVAILABLE');
      await controller.refreshVpnEligibility();
      expect(controller.vpnState, VpnState.paused);
      expect(controller.activeResource!.expiresAtUtc, before);
      expect(vpn.pauses, hasLength(1));
    },
  );

  test(
    'update completion from an old operation cannot overwrite logout',
    () async {
      await connected();
      final gate = Completer<VpnSnapshot>(), started = Completer<void>();
      vpn.onUpdate = (_) {
        started.complete();
        return gate.future;
      };
      final refresh = controller.refreshVpnEligibility();
      await Future.any([started.future, refresh]);
      expect(vpn.updates, hasLength(1));
      final logout = controller.logout();
      gate.complete(
        snapshot(
          generation: 9,
          state: 'connected',
          request: vpn.current.requestId,
        ),
      );
      await refresh;
      await logout;
      expect(controller.customer, isNull);
      expect(controller.vpnState, VpnState.idle);
      expect(controller.activeResource, isNull);
    },
  );

  for (final (initial, renewed) in [(null, 5), (20, 5), (5, 12), (5, null)]) {
    test(
      'authoritative lease $initial -> $renewed updates native expiry and accepted metadata',
      () async {
        controller.dispose();
        var now = testNow;
        controller = AppController(
          source: source,
          store: MemoryStore(),
          vpnClient: vpn,
          clock: () => now,
        );
        source.onNode = (_) async => freshNode(
          expiry: initial == null
              ? null
              : testNow.add(Duration(seconds: initial)),
        );
        await connected();
        now = testNow.add(const Duration(seconds: 2));
        source.onNode = (_) async => freshNode(
          expiry: renewed == null
              ? null
              : testNow.add(Duration(seconds: renewed)),
        );
        await controller.refreshVpnEligibility();
        expect(vpn.updates, hasLength(1));
        expect(
          (jsonDecode(vpn.updates.single['config']!) as Map)['expiresAtUtc'],
          renewed == null
              ? null
              : testNow.add(Duration(seconds: renewed)).toIso8601String(),
        );
        expect(
          controller.activeResource!.expiresAtUtc,
          renewed == null ? null : testNow.add(Duration(seconds: renewed)),
        );
        expect(
          (jsonDecode(vpn.updates.single['config']!) as Map)['effectiveNowUtc'],
          '2026-10-13T00:00:02.000Z',
        );
        expect(controller.vpnState, VpnState.connected);
      },
    );
  }

  test(
    'failed sync preserves native lease and native expiry event pauses UI',
    () async {
      source.onNode = (_) async =>
          freshNode(expiry: testNow.add(const Duration(seconds: 5)));
      await connected();
      source.onNode = (_) async => throw const ApiFailure('network_error');
      await controller.refreshVpnEligibility();
      expect(controller.vpnState, VpnState.connected);
      expect(
        controller.activeResource!.expiresAtUtc,
        testNow.add(const Duration(seconds: 5)),
      );
      vpn.emit(
        snapshot(
          generation: 10,
          state: 'paused',
          request: vpn.current.requestId,
          errorCode: 'RESOURCE_EXPIRED',
        ),
      );
      expect(controller.vpnState, VpnState.paused);
      expect(vpn.updates, isEmpty);
    },
  );

  test('late exit probe cannot undo native CONFIG_CHANGED pause', () async {
    await connected();
    final gate = Completer<VpnSnapshot>();
    final old = vpn.current;
    vpn.onObserve = () => gate.future;
    final probing = controller.observeExit();
    vpn.onUpdate = (_) async => vpn.current = snapshot(
      generation: old.generation + 1,
      state: 'paused',
      request: old.requestId,
      errorCode: 'CONFIG_CHANGED',
    );
    await controller.refreshVpnEligibility();
    gate.complete(
      snapshot(
        generation: 999,
        state: 'connected',
        request: old.requestId,
        observation: 'available',
        ip: '8.8.8.8',
      ),
    );
    await probing;
    expect(controller.vpnState, VpnState.paused);
    expect(controller.vpnSnapshot!.errorCode, 'CONFIG_CHANGED');
    expect(controller.observedExitIp, isNull);
  });

  test(
    'late getNode refresh cannot dispatch update for a replacement request',
    () async {
      await connected();
      final gate = Completer<NodeRecord>();
      source.onNode = (_) => gate.future;
      final refreshing = controller.refreshVpnEligibility();
      source.onNode = null;
      controller.select(controller.resources.last);
      await controller.connect();
      gate.complete(freshNode(expiry: testNow.add(const Duration(days: 90))));
      await refreshing;
      expect(vpn.updates, isEmpty);
      expect(controller.vpnSnapshot!.requestId, vpn.requests.last.requestId);
      expect(controller.activeResource!.key.sourceId, '2');
    },
  );

  test(
    'native update reply cannot roll back a newer native error event',
    () async {
      await connected();
      final old = vpn.current;
      vpn.onUpdate = (_) async {
        vpn.emit(
          snapshot(
            generation: old.generation + 2,
            state: 'error',
            request: old.requestId,
            errorCode: 'CLEANUP_FAILED',
          ),
        );
        return snapshot(
          generation: old.generation + 1,
          state: 'connected',
          request: old.requestId,
        );
      };
      await controller.refreshVpnEligibility();
      expect(controller.vpnState, VpnState.error);
      expect(controller.vpnSnapshot!.errorCode, 'CLEANUP_FAILED');
      expect(controller.vpnSnapshot!.generation, old.generation + 2);
    },
  );

  testWidgets('API outage beyond 72 hours adds no offline cutoff', (
    tester,
  ) async {
    final offline = AppController(
      source: source,
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
      vpnRefreshInterval: const Duration(days: 10),
    );
    await offline.initialize();
    await offline.connect();
    vpn.emit(
      snapshot(
        generation: 3,
        state: 'connected',
        request: vpn.requests.single.requestId,
      ),
    );
    source.onNode = (_) async => throw const ApiFailure('network_error');
    await offline.refreshVpnEligibility();
    await tester.pump(const Duration(hours: 73));
    expect(offline.vpnState, VpnState.connected);
    expect(offline.activeResource!.expiresAtUtc, node('1').expiresAtUtc);
    expect(vpn.pauses, isEmpty);
    expect(vpn.updates, isEmpty);
    offline.dispose();
  });
}

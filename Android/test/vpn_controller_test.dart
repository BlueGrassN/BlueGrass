import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

void main() {
  late FakeSource source;
  late FakeVpnClient vpn;
  late AppController controller;
  late MemoryStore store;
  setUp(() {
    source = FakeSource()..customer = testCustomer;
    vpn = FakeVpnClient();
    store = MemoryStore();
    controller = AppController(
      source: source,
      store: store,
      vpnClient: vpn,
      clock: () => testNow,
    );
  });
  tearDown(() async {
    controller.dispose();
    await vpn.events.close();
  });
  Future<void> connected() async {
    await controller.initialize();
    await controller.connect();
    vpn.emit(
      snapshot(
        generation: vpn.current.generation + 1,
        state: 'connected',
        request: vpn.requests.last.requestId,
      ),
    );
  }

  test(
    'denied permission retains native state and does not fetch credentials',
    () async {
      await controller.initialize();
      vpn.permitted = false;
      source.onNode = (_) => throw StateError('must not fetch');
      await controller.connect();
      expect(vpn.preparations, 1);
      expect(vpn.requests, isEmpty);
      expect(controller.vpnState, VpnState.idle);
      expect(controller.vpnError, isNotNull);
    },
  );

  test(
    'connect refreshes selection and sends only versioned node config',
    () async {
      await controller.initialize();
      source.onNode = (id) async => NodeRecord(
        key: node(id).key,
        name: 'fresh',
        protocol: 'http',
        host: 'fresh.example.invalid',
        port: 443,
        protocolConfig: ProtocolConfig.fromJson({
          'version': 1,
          'type': 'http',
          'http': {'username': 'fixture', 'password': 'secret'},
        }),
      );
      await controller.connect();
      expect(controller.vpnState, VpnState.connecting);
      final request = vpn.requests.single;
      final config = jsonDecode(request.config) as Map;
      expect(config.keys.toSet(), {
        'version',
        'host',
        'port',
        'protocolConfig',
        'expiresAtUtc',
        'effectiveNowUtc',
        'routingMode',
      });
      expect(config['version'], 1);
      expect(config['host'], 'fresh.example.invalid');
      expect(config['port'], 443);
      expect(config['expiresAtUtc'], isNull);
      expect(config['effectiveNowUtc'], '2026-10-13T00:00:00.000Z');
      expect((config['protocolConfig'] as Map)['type'], 'http');
      expect(request.resourceKey, node('1').key.stableId);
      expect(request.accountKey, '1');
      expect(request.name, 'fresh');
      expect(
        request.requestId,
        matches(
          RegExp(
            r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
          ),
        ),
      );
      expect(store.snapshot, isNull);
      expect(request.toString(), isNot(contains('secret')));
    },
  );

  for (final invalid in ['expired', 'foreign', 'invalid']) {
    test('fresh $invalid node cannot connect', () async {
      await controller.initialize();
      source.onNode = (_) async => switch (invalid) {
        'expired' => node('1', expired: true),
        'foreign' => node('2'),
        _ => NodeRecord(
          key: node('1').key,
          name: 'bad',
          protocol: 'socks5',
          host: '',
          port: 0,
        ),
      };
      await controller.connect();
      expect(vpn.requests, isEmpty);
      expect(controller.vpnState, VpnState.idle);
      expect(controller.vpnError, isNotNull);
    });
  }

  test(
    'late permission and node response after logout cannot start VPN',
    () async {
      await controller.initialize();
      final gate = Completer<NodeRecord>();
      final started = Completer<void>();
      source.onNode = (_) {
        started.complete();
        return gate.future;
      };
      final connecting = controller.connect();
      await started.future;
      await controller.logout();
      gate.complete(node('1'));
      await connecting;
      expect(vpn.requests, isEmpty);
      expect(controller.customer, isNull);
      expect(controller.vpnState, VpnState.idle);
    },
  );

  test('logout waits for pending native connect then confirmed stop', () async {
    await controller.initialize();
    final gate = Completer<VpnSnapshot>();
    final started = Completer<void>();
    vpn.onConnect = (_) {
      started.complete();
      return gate.future;
    };
    final connecting = controller.connect();
    await started.future;
    var cleared = false;
    source.onLogout = () async {
      cleared = true;
    };
    final loggingOut = controller.logout();
    await Future<void>.delayed(Duration.zero);
    expect(cleared, isFalse);
    expect(controller.customer, isNotNull);
    vpn.current = snapshot(
      generation: 2,
      state: 'connected',
      request: vpn.requests.single.requestId,
    );
    gate.complete(vpn.current);
    await connecting;
    await loggingOut;
    expect(cleared, isTrue);
    expect(controller.vpnState, VpnState.idle);
    vpn.emit(
      snapshot(
        generation: 999,
        state: 'connected',
        request: vpn.requests.single.requestId,
      ),
    );
    expect(controller.customer, isNull);
    expect(controller.vpnState, VpnState.idle);
  });

  test(
    'stop failure blocks logout and account switch before source mutation',
    () async {
      await connected();
      var cleared = false, loggedIn = false;
      source.onLogout = () async {
        cleared = true;
      };
      source.onLogin = () async {
        loggedIn = true;
        return testCustomer;
      };
      vpn.onDisconnect = () async => throw const VpnFailure('STOP_FAILED');
      await controller.logout();
      expect(cleared, isFalse);
      expect(controller.customer, testCustomer);
      expect(controller.authError, isNotNull);
      await controller.login('other', 'fixture');
      expect(loggedIn, isFalse);
      expect(controller.customer, testCustomer);
      vpn.onDisconnect = null;
      await controller.logout();
      expect(cleared, isTrue);
    },
  );

  test(
    'same account service recovery restores active resource and validates it',
    () async {
      vpn.current = snapshot(
        state: 'connected',
        resource: node('2').key.stableId,
      );
      final fetched = <String>[];
      source.onNode = (id) async {
        fetched.add(id);
        return node(id);
      };
      await controller.initialize();
      expect(controller.vpnState, VpnState.connected);
      expect(controller.activeResource?.key.sourceId, '2');
      expect(fetched, contains('2'));
      expect(vpn.requests, isEmpty);
      expect(vpn.disconnects, 0);
    },
  );

  test('foreign runtime is stopped before loading account resources', () async {
    vpn.current = snapshot(state: 'connected', account: 'other');
    source.onNodes = (_) async {
      expect(vpn.current.state, VpnState.idle);
      return Page(items: [node('1')], hasMore: false);
    };
    await controller.initialize();
    expect(vpn.disconnects, 1);
    expect(controller.vpnState, VpnState.idle);
  });

  test(
    'resume reads new runtime and rejects retired runtime and stale generation',
    () async {
      await connected();
      final old = vpn.current;
      vpn.current = snapshot(
        runtime: '00000000-0000-4000-8000-000000000020',
        state: 'connected',
      );
      await controller.resumeVpn();
      expect(controller.vpnSnapshot?.runtimeId, vpn.current.runtimeId);
      vpn.emit(old);
      expect(
        controller.vpnSnapshot?.runtimeId,
        '00000000-0000-4000-8000-000000000020',
      );
      vpn.emit(
        snapshot(
          runtime: '00000000-0000-4000-8000-000000000020',
          generation: 0,
          state: 'paused',
        ),
      );
      expect(controller.vpnState, VpnState.connected);
    },
  );

  for (final invalid in ['expired', 'revoked']) {
    test(
      'eligibility refresh pauses $invalid active session preserving account',
      () async {
        await connected();
        source.onNode = (_) async {
          if (invalid == 'revoked') {
            throw const ApiFailure('node_not_found', status: 404);
          }
          return node('1', expired: true);
        };
        await controller.refreshVpnEligibility();
        expect(controller.vpnState, VpnState.paused);
        expect(controller.vpnSnapshot?.tunActive, isTrue);
        expect(controller.customer, testCustomer);
        expect(vpn.pauses, hasLength(1));
      },
    );
  }

  test(
    'failed observation stays connected and late probe cannot undo logout',
    () async {
      await connected();
      vpn.onObserve = () async => throw const VpnFailure('EXIT_PROBE_FAILED');
      await controller.observeExit();
      expect(controller.vpnState, VpnState.connected);
      expect(controller.observationState, VpnObservationState.failed);
      final probe = Completer<VpnSnapshot>();
      vpn.onObserve = () => probe.future;
      final observing = controller.observeExit();
      final old = vpn.current;
      await controller.logout();
      probe.complete(
        snapshot(
          generation: old.generation + 10,
          state: 'connected',
          request: old.requestId,
          observation: 'available',
          ip: '8.8.8.8',
        ),
      );
      await observing;
      expect(controller.vpnState, VpnState.idle);
      expect(controller.observedExitIp, isNull);
    },
  );

  test(
    'connection switch reauthorizes and late prior request event is ignored',
    () async {
      await connected();
      final old = vpn.current;
      controller.select(controller.resources.last);
      await controller.connect();
      expect(vpn.preparations, 2);
      expect(vpn.requests, hasLength(2));
      expect(vpn.requests.last.resourceKey, node('2').key.stableId);
      vpn.emit(
        snapshot(generation: 999, state: 'connected', request: old.requestId),
      );
      expect(controller.vpnSnapshot?.requestId, vpn.requests.last.requestId);
    },
  );

  test(
    'resume from permission dialog does not cancel authorized connect',
    () async {
      await controller.initialize();
      final permission = Completer<bool>();
      vpn.onPrepare = () => permission.future;
      final connecting = controller.connect();
      await controller.resumeVpn();
      permission.complete(true);
      await connecting;
      expect(vpn.requests, hasLength(1));
      expect(controller.vpnState, VpnState.connecting);
    },
  );

  test('failed native switch recovers original request and keeps receiving its events', () async {
    await connected();
    final old = vpn.current;
    controller.select(controller.resources.last);
    vpn.onConnect = (_) async => throw const VpnFailure('INVALID_CONFIG');
    await controller.connect();
    vpn.emit(
      snapshot(
        generation: old.generation + 2,
        state: 'paused',
        request: old.requestId,
      ),
    );
    expect(controller.vpnState, VpnState.paused);
    expect(controller.vpnSnapshot?.requestId, old.requestId);
    expect(controller.activeResource?.key.sourceId, '1');
  });

  test(
    'idle stop snapshot without newer generation cannot authorize logout',
    () async {
      await connected();
      vpn.onDisconnect = () async =>
          snapshot(generation: vpn.current.generation);
      await controller.logout();
      expect(controller.customer, testCustomer);
      expect(source.customer, testCustomer);
      expect(controller.authError, isNotNull);
    },
  );

  test('late probe after eligibility pause cannot restore connected', () async {
    await connected();
    final probe = Completer<VpnSnapshot>();
    final old = vpn.current;
    vpn.onObserve = () => probe.future;
    final observing = controller.observeExit();
    source.onNode = (_) async => node('1', expired: true);
    await controller.refreshVpnEligibility();
    probe.complete(
      snapshot(
        generation: 999,
        state: 'connected',
        request: old.requestId,
        observation: 'available',
        ip: '8.8.8.8',
      ),
    );
    await observing;
    expect(controller.vpnState, VpnState.paused);
    expect(controller.observedExitIp, isNull);
    expect(controller.observationState, isNot(VpnObservationState.checking));
  });

  test(
    'late prior account event after next login cannot restore its session',
    () async {
      await connected();
      final old = vpn.current;
      await controller.logout();
      await controller.login('demo', 'fixture');
      vpn.emit(
        snapshot(generation: 999, state: 'connected', request: old.requestId),
      );
      expect(controller.vpnState, VpnState.idle);
    },
  );

  test('a native error snapshot never claims connected', () async {
    await controller.initialize();
    await controller.connect();
    vpn.emit(
      snapshot(
        generation: 10,
        state: 'error',
        request: vpn.requests.single.requestId,
      ),
    );
    expect(controller.vpnState, VpnState.error);
    expect(controller.observedExitIp, isNull);
  });

  test(
    'newer event arriving during status recovery wins over its stale reply',
    () async {
      await controller.initialize();
      final gate = Completer<VpnSnapshot>();
      final started = Completer<void>();
      vpn.onStatus = () {
        started.complete();
        return gate.future;
      };
      final recovering = controller.resumeVpn();
      await started.future;
      vpn.emit(snapshot(generation: 4, state: 'connected'));
      gate.complete(snapshot(generation: 3, state: 'connecting'));
      await recovering;
      expect(controller.vpnState, VpnState.connected);
      expect(controller.vpnSnapshot?.generation, 4);
    },
  );

  test('denied switch leaves the existing session connected', () async {
    await connected();
    final old = vpn.current;
    controller.select(controller.resources.last);
    vpn.permitted = false;
    await controller.connect();
    expect(controller.vpnSnapshot, same(old));
    expect(vpn.requests, hasLength(1));
    expect(controller.activeResource?.key.sourceId, '1');
  });

  test('logout invalidates late permission result', () async {
    await controller.initialize();
    final gate = Completer<bool>();
    vpn.onPrepare = () => gate.future;
    final connecting = controller.connect();
    await controller.logout();
    gate.complete(true);
    await connecting;
    expect(vpn.requests, isEmpty);
    expect(controller.customer, isNull);
  });

  test('old eligibility failure cannot pause newly selected session', () async {
    await connected();
    final gate = Completer<NodeRecord>();
    source.onNode = (_) => gate.future;
    final refresh = controller.refreshVpnEligibility();
    source.onNode = null;
    controller.select(controller.resources.last);
    await controller.connect();
    gate.completeError(const ApiFailure('node_not_found', status: 404));
    await refresh;
    expect(vpn.pauses, isEmpty);
    expect(controller.vpnSnapshot?.resourceKey, node('2').key.stableId);
  });

  test(
    'active resource stays on native session until switch is accepted',
    () async {
      await connected();
      final gate = Completer<VpnSnapshot>();
      final started = Completer<void>();
      vpn.onConnect = (_) {
        started.complete();
        return gate.future;
      };
      controller.select(controller.resources.last);
      final switching = controller.connect();
      await started.future;
      expect(controller.activeResource?.key.sourceId, '1');
      gate.complete(
        snapshot(
          generation: 10,
          state: 'connecting',
          request: vpn.requests.last.requestId,
          resource: node('2').key.stableId,
        ),
      );
      await switching;
      expect(controller.activeResource?.key.sourceId, '2');
    },
  );
}

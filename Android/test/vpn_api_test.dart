import 'dart:convert';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter_test/flutter_test.dart';

import 'api_flow_test.dart' show ContractTransport;
import 'support/fakes.dart';
import 'support/fake_vpn.dart';

void main() {
  for (final trusted in [true, false]) {
    test(
      'actual API adapter ${trusted ? 'uses server time instead of injected clock' : 'refuses absent effective server time'}',
      () async {
        final store = MemoryStore();
        final transport = ContractTransport();
        final source = ApiV2DataSource(
          baseUri: Uri.parse(
            trusted
                ? 'https://example.invalid/api/client/v1'
                : 'http://127.0.0.1/api/client/v1',
          ),
          store: store,
          transport: transport,
          clock: () => testNow,
          allowLoopbackHttp: !trusted,
        );
        final vpn = FakeVpnClient();
        final controller = AppController(
          source: source,
          store: store,
          vpnClient: vpn,
          clock: () => DateTime.utc(2036),
        );
        addTearDown(controller.dispose);
        await controller.initialize();
        await controller.login('demo', 'FAKE_LOGIN_PASSWORD');
        await controller.connect();
        if (trusted) {
          expect(controller.vpnState, VpnState.connecting);
          vpn.emit(
            snapshot(
              generation: vpn.current.generation + 1,
              state: 'connected',
              request: vpn.requests.single.requestId,
              resource: vpn.requests.single.resourceKey,
            ),
          );
          expect(controller.vpnState, VpnState.connected);
          await controller.resumeVpn();
          expect(controller.vpnState, VpnState.connected);
          final config = jsonDecode(vpn.requests.single.config) as Map;
          final now = DateTime.parse(config['effectiveNowUtc'] as String);
          expect(now.difference(testNow).inSeconds, inInclusiveRange(0, 30));
          final wire = jsonEncode(vpn.requests.single.toMap());
          expect(wire, isNot(contains('FAKE_ACCESS')));
          expect(wire, isNot(contains('FAKE_REFRESH')));
          expect(wire, isNot(contains('FAKE_LOGIN_PASSWORD')));
          expect(wire, isNot(contains('FAKE_SESSION')));
          expect(
            jsonEncode(store.snapshot),
            isNot(contains('FAKE_NODE_PASSWORD')),
          );
        } else {
          expect(source.effectiveNowUtc, isNull);
          expect(vpn.requests, isEmpty);
          expect(controller.vpnError, contains('服务端时间'));
        }
      },
    );
  }
}

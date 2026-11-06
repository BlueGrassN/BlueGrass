import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_client/control_http_transport.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter/services.dart';
import 'package:integration_test/integration_test.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'GUI method channel performs physically bound HTTPS health read',
    (tester) async {
      const ip = String.fromEnvironment('BLUEGRASS_CONTROL_HTTP_SMOKE_IP');
      final address = InternetAddress.tryParse(ip);
      expect(
        address,
        isNotNull,
        reason: 'Explicit resolved smoke destination required',
      );
      expect(address!.type, InternetAddressType.IPv4);
      final transport = ControlHttpTransport(
        resolveHost: (host) async {
          expect(host, 'client-api.sunip.vip');
          return [address];
        },
      );
      try {
        await transport.configure(
          Uri.parse('https://client-api.sunip.vip/api/client/v1'),
        );
        // Native startup pin is immutable after configuration, independently
        // of the Dart transport's own endpoint checks.
        await expectLater(
          const MethodChannel('bluegrass/platform').invokeMethod<void>(
            'controlHttpConfigure',
            {'host': 'api-next.sunip.vip'},
          ),
          throwsA(isA<PlatformException>()),
        );
        final response = await transport.send(
          ApiRequest(
            method: 'GET',
            uri: Uri.parse('https://client-api.sunip.vip/api/client/v1/health'),
          ),
        );
        expect(response.status, 200);
        final body = jsonDecode(utf8.decode(response.body)) as Map;
        expect(body['data']['status'], 'ok');
      } finally {
        transport.dispose();
      }
    },
    timeout: const Timeout(Duration(seconds: 40)),
  );
}

import 'package:bluegrass_client/control_http_transport.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'compiled foreign API fails at construction before even unprotected IO',
    () {
      const origin = String.fromEnvironment(
        'BLUEGRASS_API_URL',
        defaultValue: 'https://client-api.sunip.vip/api/client/v1',
      );
      ControlHttpTransport make() => ControlHttpTransport(
        resolveHost: (_) async => null,
        protectionActive: () => false,
        unprotectedTransport: IoHttpTransport(),
      );
      if (origin == 'https://client-api.sunip.vip/api/client/v1') {
        make().dispose();
      } else {
        expect(
          make,
          throwsA(
            isA<ApiFailure>().having(
              (e) => e.code,
              'code',
              'unsupported_api_build',
            ),
          ),
        );
      }
    },
  );
}

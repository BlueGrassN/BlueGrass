import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:client/platform/android_store.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('bluegrass/android');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  tearDown(() => messenger.setMockMethodCallHandler(channel, null));
  test('session crosses channel as detached JSON and clears', () async {
    String? value;
    messenger.setMockMethodCallHandler(channel, (call) async {
      switch (call.method) {
        case 'sessionRead':
          return value;
        case 'sessionReplace':
          value = (call.arguments as Map)['value'] as String;
          return null;
        case 'sessionClear':
          value = null;
          return null;
      }
      throw MissingPluginException();
    });
    final store = AndroidStore();
    await store.replace({
      'generation': 3,
      'nested': {'v': 'fixture'},
    });
    final read = await store.read();
    (read!['nested'] as Map)['v'] = 'changed';
    expect((await store.read())!['nested'], {'v': 'fixture'});
    await store.clear();
    expect(await store.read(), isNull);
  });
  test(
    'malformed record and keystore failure never become empty success',
    () async {
      messenger.setMockMethodCallHandler(channel, (_) async => '[]');
      final store = AndroidStore();
      await expectLater(store.read(), throwsA(isA<ApiFailure>()));
      messenger.setMockMethodCallHandler(
        channel,
        (_) async => throw PlatformException(
          code: 'storage_error',
          message: 'private detail',
        ),
      );
      await expectLater(
        store.read(),
        throwsA(
          isA<ApiFailure>().having((e) => e.code, 'code', 'storage_error'),
        ),
      );
    },
  );
}

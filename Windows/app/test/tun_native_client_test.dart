import 'dart:io';

import 'package:bluegrass_client/native_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'update detach pauses and closes broker without releasing protection',
    () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final client = NativeClient(
        startProcess: (_, _) => Process.start(dart, [
          File('test/utils/native_process_fixture.dart').absolute.path,
          'detach-tun',
        ]),
      );
      await client.start(mode: 'tun');
      await client.detachForUpdate();
      expect(client.available, isFalse);
    },
  );
  test(
    'TUN launch preserves recovered protection until explicit disconnect',
    () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final client = NativeClient(
        startProcess: (path, args) {
          expect(args, ['--tun']);
          return Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            'recovered-tun',
          ]);
        },
      );
      final hello = await client.start(mode: 'tun');
      expect(hello['snapshot']['protectionIntent'], isTrue);
      expect((await client.status())['snapshot']['protectionIntent'], isTrue);
      await client.stop();
      expect(client.available, isFalse);
    },
  );
  test(
    'protection remaining after Disconnect cannot count as restored',
    () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final client = NativeClient(
        startProcess: (path, args) => Process.start(dart, [
          File('test/utils/native_process_fixture.dart').absolute.path,
          'disconnect-protected-tun',
        ]),
      );
      await client.start(mode: 'tun');
      await expectLater(client.stop(), throwsA(isA<NativeClientException>()));
    },
  );
}

import 'dart:async';
import 'dart:io';

import 'package:bluegrass_client/diagnostic_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  DiagnosticClient client(String scenario, {Completer<void>? started}) {
    final dart = File(Platform.resolvedExecutable).parent.uri
        .resolve(
          '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
        )
        .toFilePath();
    return DiagnosticClient(
      startProcess: (_, args) async {
        expect(args, isEmpty);
        final process = await Process.start(dart, [
          File('test/utils/diagnostic_process_fixture.dart').absolute.path,
          scenario,
        ]);
        started?.complete();
        return process;
      },
    );
  }

  test('diagnostic helper returns bounded structured results', () async {
    final report = await client('valid').run({});
    expect(report['results'], hasLength(1));
  });
  test('oversized diagnostic response rejected', () async {
    await expectLater(client('oversized').run({}), throwsFormatException);
  });
  test('cancellation rejects late diagnostic result', () async {
    final started = Completer<void>();
    final helper = client('wait', started: started);
    final result = helper.run({});
    final expectation = expectLater(result, throwsFormatException);
    await started.future;
    helper.cancel();
    await expectation;
  });
}

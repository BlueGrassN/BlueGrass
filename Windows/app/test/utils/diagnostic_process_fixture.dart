import 'dart:async';
import 'dart:convert';
import 'dart:io';

Future<void> main(List<String> args) async {
  await stdin.transform(utf8.decoder).join();
  if (args.single == 'wait') {
    await Future<void>.delayed(const Duration(seconds: 30));
  }
  if (args.single == 'oversized') {
    stdout.writeln('x' * 20000);
    return;
  }
  stdout.writeln(
    jsonEncode({
      'version': 1,
      'results': [
        {'id': 'api_health', 'status': 'skipped', 'code': 'not_configured'},
      ],
    }),
  );
}

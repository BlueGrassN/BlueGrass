import 'dart:io';

// Runs mutated copies only; never alters sources being consumed by the app.
Future<void> main() async {
  final root = Directory.current.absolute;
  if (!File('${root.path}/lib/bluegrass_data_source.dart').existsSync()) {
    throw StateError('Run from the data_source package');
  }
  final scratch = Directory('${root.path}/.dart_tool/ablation');
  scratch.createSync(recursive: true);
  final target = Directory(
    '${scratch.path}/run-${DateTime.now().microsecondsSinceEpoch}',
  );
  target.createSync();
  for (final name in ['lib', 'test']) {
    final source = Directory('${root.path}/$name');
    for (final entity in source.listSync(recursive: true)) {
      final destination =
          '${target.path}/${entity.path.substring(root.path.length + 1)}';
      if (entity is File) {
        File(destination).parent.createSync(recursive: true);
        entity.copySync(destination);
      }
    }
  }
  final domain = Directory(
    '${root.path}/../domain',
  ).absolute.path.replaceAll('\\', '/');
  File('${target.path}/pubspec.yaml').writeAsStringSync(
    File(
      '${root.path}/pubspec.yaml',
    ).readAsStringSync().replaceFirst('path: ../domain', "path: '$domain'"),
  );
  File('${root.path}/pubspec.lock').copySync('${target.path}/pubspec.lock');
  final setup = await Process.run(Platform.resolvedExecutable, [
    'pub',
    'get',
    '--offline',
  ], workingDirectory: target.path);
  if (setup.exitCode != 0)
    throw StateError('Ablation dependency resolution failed');
  final session = File('${target.path}/lib/src/session_coordinator.dart');
  final original = session.readAsStringSync().replaceAll('\r\n', '\n');
  final cases = <(String, String, String, String)>[
    (
      'single-flight',
      'if (_refresh != null) return _refresh!;',
      '',
      'concurrent_requests_share_one_refresh',
    ),
    (
      'generation isolation',
      "if (generation != _generation) throw const ApiFailure('session_changed');",
      '',
      'late_refresh_cannot_overwrite_login',
    ),
    (
      'durable pending record',
      "snapshot = {...snapshot, 'pending_refresh': pending};\n      await _save(generation, snapshot);",
      "snapshot = {...snapshot, 'pending_refresh': pending};",
      'pending_uuid_is_durable_before_refresh_network',
    ),
    (
      'restore single-flight',
      'if (_restore != null) return _restore!;',
      '',
      'restore callers',
    ),
  ];
  final lines = <String>[];
  final docs = Directory('${root.path}/docs');
  docs.createSync(recursive: true);
  for (final (name, before, after, test) in cases) {
    if (!original.contains(before))
      throw StateError('Mutation marker changed: $name');
    session.writeAsStringSync(original.replaceFirst(before, after));
    final result = await Process.run(Platform.resolvedExecutable, [
      'test',
      'test/session_coordinator_test.dart',
      '--name',
      test,
      '--timeout',
      '5s',
      '--reporter',
      'expanded',
    ], workingDirectory: target.path);
    final output = '${result.stdout}\n${result.stderr}';
    final killed =
        result.exitCode != 0 &&
        output.contains('Expected:') &&
        !output.contains('Failed to load');
    final line =
        '$name: ${killed ? "behavior regression detected" : "UNEXPECTED RESULT"} (exit ${result.exitCode})';
    lines.add(line);
    stdout.writeln(line);
    File(
      '${docs.path}/ablation-${name.replaceAll(' ', '-')}.txt',
    ).writeAsStringSync(output);
    if (!killed) exitCode = 1;
  }
  session.writeAsStringSync(original);
  File(
    '${docs.path}/ablation-results.txt',
  ).writeAsStringSync('${lines.join('\n')}\n');
}

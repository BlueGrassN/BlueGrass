import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'desktop_platform.dart';

/// A one-shot, unprivileged helper. Cancellation terminates only this exact
/// child; no installed service or external proxy process is controlled.
class DiagnosticClient {
  DiagnosticClient({
    Future<Process> Function(String, List<String>)? startProcess,
  }) : _startProcess =
           startProcess ??
           ((exe, args) => Process.start(exe, args, runInShell: false));
  final Future<Process> Function(String, List<String>) _startProcess;
  Process? _process;
  int _generation = 0;
  void cancel() {
    ++_generation;
    _process?.kill();
  }

  Future<Map<String, dynamic>> run(Map<String, dynamic> request) async {
    cancel();
    final generation = _generation;
    final executable = DesktopPlatform.current.helperPath(
      'BlueGrass.Diagnostics',
    );
    final process = await _startProcess(executable, const []);
    if (generation != _generation) {
      process.kill();
      throw const FormatException('DIAGNOSTIC_CANCELLED');
    }
    _process = process;
    final errorDrain = process.stderr.drain<void>();
    try {
      final output = <int>[];
      var oversized = false;
      final collected = process.stdout.listen((chunk) {
        if (output.length + chunk.length > 16384) {
          oversized = true;
          process.kill();
        } else {
          output.addAll(chunk);
        }
      }).asFuture<void>();
      process.stdin.writeln(jsonEncode(request));
      await process.stdin.close();
      final exit = await process.exitCode.timeout(const Duration(seconds: 35));
      await collected;
      if (generation != _generation || exit != 0 || oversized) {
        throw const FormatException('DIAGNOSTIC_FAILED');
      }
      final result = jsonDecode(utf8.decode(output));
      if (result is! Map<String, dynamic> ||
          result['version'] != 1 ||
          result['results'] is! List ||
          (result['results'] as List).length > 24) {
        throw const FormatException('DIAGNOSTIC_INVALID_REPORT');
      }
      return result;
    } finally {
      process.kill();
      await errorDrain;
      if (identical(_process, process)) _process = null;
    }
  }
}

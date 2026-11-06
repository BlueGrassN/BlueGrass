import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'desktop_platform.dart';

import 'package:flutter/foundation.dart';

typedef UpdateProcessStarter = Future<Process> Function(
  String executable,
  List<String> arguments,
);

enum UpdateState { upToDate, available, downloading, ready, launchRequested }

enum UpdateStartupPolicyState { noHistory, verified }

/// Offline decision reverified by the installed helper. An expired signed
/// history can restrict connections but carries no download/install authority.
class UpdateStartupPolicy {
  const UpdateStartupPolicy({
    required this.state,
    this.minimumVersion = '',
    this.unsupported = false,
    this.manifestExpired = false,
  });
  final UpdateStartupPolicyState state;
  final String minimumVersion;
  final bool unsupported, manifestExpired;

  static UpdateStartupPolicy _decode(Object? value) {
    if (value is! Map<String, dynamic> ||
        value.values.any((v) => v == null) ||
        value.keys.any(
          (k) => !const {
            'state',
            'minimumVersion',
            'unsupported',
            'manifestExpired',
          }.contains(k),
        ) ||
        value['unsupported'] is! bool ||
        value['manifestExpired'] is! bool) {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    }
    final state = switch (value['state']) {
      'no_history' => UpdateStartupPolicyState.noHistory,
      'verified' => UpdateStartupPolicyState.verified,
      _ => throw const UpdateException('UPDATE_PROTOCOL_ERROR'),
    };
    final minimum = value['minimumVersion'] ?? '';
    if (minimum is! String ||
        minimum.length > 80 ||
        (state == UpdateStartupPolicyState.noHistory
            ? minimum.isNotEmpty ||
                  value['unsupported'] != false ||
                  value['manifestExpired'] != false
            : !_policyVersion.hasMatch(minimum))) {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    }
    return UpdateStartupPolicy(
      state: state,
      minimumVersion: minimum,
      unsupported: value['unsupported'] as bool,
      manifestExpired: value['manifestExpired'] as bool,
    );
  }
}

final _policyVersion = RegExp(
  r'^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$',
);

class UpdateException implements Exception {
  const UpdateException(this.code);
  final String code;
  @override
  String toString() => 'UpdateException($code)';
}

/// Validated helper state. candidateId is opaque; no path, feed or trust key is
/// exposed. launchRequested means process creation, never installation success.
class UpdateSnapshot {
  const UpdateSnapshot({
    required this.state,
    this.candidateId,
    this.version,
    this.build = 0,
    this.size = 0,
    this.notes = '',
    this.minimumVersion = '',
    this.unsupported = false,
    this.downloaded = 0,
    this.remaining = 0,
    this.fromCache = false,
  });
  final UpdateState state;
  final String? candidateId, version;
  final int build, size, downloaded, remaining;
  final String notes, minimumVersion;
  final bool unsupported, fromCache;

  static UpdateSnapshot _decode(Object? value) {
    if (value is! Map<String, dynamic> ||
        value.keys.any(
          (k) => !const {
            'state',
            'candidateId',
            'version',
            'build',
            'size',
            'notes',
            'minimumVersion',
            'unsupported',
            'downloaded',
            'remaining',
            'fromCache',
          }.contains(k),
        )) {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    }
    final state = switch (value['state']) {
      'up_to_date' => UpdateState.upToDate,
      'available' => UpdateState.available,
      'downloading' => UpdateState.downloading,
      'ready' => UpdateState.ready,
      'launch_requested' => UpdateState.launchRequested,
      _ => throw const UpdateException('UPDATE_PROTOCOL_ERROR'),
    };
    final id = value['candidateId'];
    final version = value['version'];
    final build = value['build'] ?? 0;
    final size = value['size'] ?? 0;
    final downloaded = value['downloaded'];
    final remaining = value['remaining'];
    final notes = value['notes'] ?? '';
    final minimum = value['minimumVersion'] ?? '';
    if (value['unsupported'] is! bool ||
        value['fromCache'] is! bool ||
        build is! int ||
        build < 0 ||
        build > 9007199254740991 ||
        size is! int ||
        size < 0 ||
        size > 512 * 1024 * 1024 ||
        downloaded is! int ||
        downloaded < 0 ||
        downloaded > size ||
        remaining is! int ||
        remaining != size - downloaded ||
        notes is! String ||
        notes.length > 16384 ||
        minimum is! String ||
        minimum.length > 80 ||
        (state == UpdateState.upToDate
            ? id != null || version != null || size != 0 || build != 0
            : id is! String ||
                  !_candidate.hasMatch(id) ||
                  version is! String ||
                  version.isEmpty ||
                  version.length > 80 ||
                  size == 0 ||
                  build == 0) ||
        (state == UpdateState.ready && downloaded != size)) {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    }
    return UpdateSnapshot(
      state: state,
      candidateId: id as String?,
      version: version as String?,
      build: build,
      size: size,
      notes: notes,
      minimumVersion: minimum,
      unsupported: value['unsupported'] as bool,
      downloaded: downloaded,
      remaining: remaining,
      fromCache: value['fromCache'] as bool,
    );
  }
}

final _candidate = RegExp(r'^[a-f0-9]{64}$');

/// One bounded request per exact adjacent BlueGrass.Update.exe child. The signed
/// helper owns release config, persisted floors, cache identities and UAC staging.
/// UI must never auto-call launch after check/download/restart. No service or
/// connection command is sent here. Install pause/shutdown belongs to main.
class UpdateClient {
  UpdateClient({
    this.startupPolicyTimeout = const Duration(seconds: 8),
    this.checkTimeout = const Duration(minutes: 3),
    this.downloadTimeout = const Duration(minutes: 16),
    this.launchTimeout = const Duration(minutes: 4),
  }) : _startProcess = _spawn;

  @visibleForTesting
  UpdateClient.withProcessStarter(
    this._startProcess, {
    this.startupPolicyTimeout = const Duration(seconds: 8),
    this.checkTimeout = const Duration(minutes: 3),
    this.downloadTimeout = const Duration(minutes: 16),
    this.launchTimeout = const Duration(minutes: 4),
  });

  final UpdateProcessStarter _startProcess;
  final Duration startupPolicyTimeout,
      checkTimeout,
      downloadTimeout,
      launchTimeout;
  final _progress = StreamController<UpdateSnapshot>.broadcast();
  final _random = Random.secure();
  Process? _process;
  bool _busy = false, _disposed = false, _cancelled = false;

  Stream<UpdateSnapshot> get progress => _progress.stream;
  bool get busy => _busy;

  Future<UpdateSnapshot> check() =>
      _snapshotCall('check', const {}, checkTimeout);

  /// No feed/service DNS/NLM access. Corrupt state is UPDATE_STATE_INVALID,
  /// never noHistory; empty embedded release config is UPDATE_UNCONFIGURED.
  Future<UpdateStartupPolicy> readStartupPolicy() =>
      _call('startup_policy', const {}, startupPolicyTimeout, (reply) {
        if (reply.containsKey('snapshot')) {
          throw const UpdateException('UPDATE_PROTOCOL_ERROR');
        }
        return UpdateStartupPolicy._decode(reply['startupPolicy']);
      });

  /// Unknown network cost waits unless the user allows this exact package.
  /// The consent is not retained for any later operation or package.
  Future<UpdateSnapshot> download(
    String candidateId, {
    bool allowMetered = false,
  }) => _candidateCall('download', candidateId, {
    'allowMetered': allowMetered,
  }, downloadTimeout);

  /// Explicit install action only. UAC cancellation is distinct from success.
  /// Cancelling/timing out after elevation has an unknown launch outcome and
  /// must never trigger an automatic retry or claim the installer did not start.
  Future<UpdateSnapshot> launch(
    String candidateId, {
    required bool installConsent,
  }) {
    if (!installConsent) {
      return Future.error(
        const UpdateException('UPDATE_INSTALL_CONSENT_REQUIRED'),
      );
    }
    return _candidateCall('launch', candidateId, const {
      'installConsent': true,
    }, launchTimeout);
  }

  Future<UpdateSnapshot> _candidateCall(
    String op,
    String id,
    Map<String, dynamic> fields,
    Duration timeout,
  ) {
    if (!_candidate.hasMatch(id)) {
      return Future.error(const UpdateException('UPDATE_CANDIDATE_INVALID'));
    }
    return _snapshotCall(op, {'candidateId': id, ...fields}, timeout);
  }

  Future<UpdateSnapshot> _snapshotCall(
    String op,
    Map<String, dynamic> fields,
    Duration timeout,
  ) => _call(op, fields, timeout, (reply) {
    if (reply.containsKey('startupPolicy')) {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    }
    final result = UpdateSnapshot._decode(reply['snapshot']);
    if ((op != 'check' && result.candidateId != fields['candidateId']) ||
        (op == 'launch' && result.state != UpdateState.launchRequested) ||
        (op == 'download' && result.state != UpdateState.ready) ||
        (op == 'check' &&
            !const {
              UpdateState.available,
              UpdateState.upToDate,
              UpdateState.ready,
            }.contains(result.state))) {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    }
    return result;
  });

  static Future<Process> _spawn(
    String executable,
    List<String> arguments,
  ) async {
    if (!Platform.isWindows) {
      throw const UpdateException('UPDATE_PLATFORM_UNSUPPORTED');
    }
    final app = File(Platform.resolvedExecutable);
    final helper = File(executable);
    // No PATH search, environment-selected executable, cwd lookup or shell.
    // The helper independently verifies KnownFolder ProgramFiles, immutable ACL,
    // its own Authenticode signer and the adjacent GUI's matching signer.
    if (app.uri.pathSegments.last.toLowerCase() != 'bluegrass.exe' ||
        (await app.resolveSymbolicLinks()).toLowerCase() !=
            app.absolute.path.toLowerCase() ||
        (await helper.resolveSymbolicLinks()).toLowerCase() !=
            helper.absolute.path.toLowerCase() ||
        helper.parent.path.toLowerCase() != app.parent.path.toLowerCase() ||
        helper.uri.pathSegments.last != 'BlueGrass.Update.exe') {
      throw const UpdateException('UPDATE_TRUST_FAILED');
    }
    return Process.start(
      executable,
      arguments,
      runInShell: false,
      workingDirectory: app.parent.path,
    );
  }

  Future<T> _call<T>(
    String op,
    Map<String, dynamic> fields,
    Duration timeout,
    T Function(Map<String, dynamic>) decode,
  ) async {
    if (!DesktopPlatform.current.supportsWindowsUpdater) {
      throw const UpdateException('UPDATE_PLATFORM_UNSUPPORTED');
    }
    if (_disposed) throw const UpdateException('UPDATE_CLOSED');
    if (_busy) throw const UpdateException('UPDATE_BUSY');
    if (timeout <= Duration.zero) {
      throw ArgumentError('Update timeout must be positive');
    }
    _busy = true;
    _cancelled = false;
    var finished = false, aborted = false, stderrOverflow = false;
    StreamSubscription<List<int>>? stderr;
    Process? child;
    final id = List.generate(
      16,
      (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
    try {
      final executable = File(Platform.resolvedExecutable).parent.uri
          .resolve('BlueGrass.Update.exe')
          .toFilePath();
      child = await _startProcess(executable, const ['--rpc']);
      _process = child;
      if (_cancelled || _disposed) {
        throw const UpdateException('UPDATE_CANCELLED');
      }
      var stderrSize = 0;
      stderr = child.stderr.listen((bytes) {
        stderrSize += bytes.length;
        if (stderrSize > 64 * 1024) {
          stderrOverflow = true;
          child?.kill();
        }
      });
      final process = child;
      Future<T> exchange() async {
        process.stdin.writeln(
          jsonEncode({'v': 1, 'id': id, 'op': op, ...fields}),
        );
        await process.stdin.close();
        final line = <int>[];
        var total = 0;
        Map<String, dynamic>? finalReply;
        await for (final chunk in process.stdout) {
          total += chunk.length;
          if (total > 8 * 1024 * 1024) {
            throw const UpdateException('UPDATE_PROTOCOL_ERROR');
          }
          for (final byte in chunk) {
            if (byte != 10) {
              line.add(byte);
              if (line.length > 64 * 1024) {
                throw const UpdateException('UPDATE_PROTOCOL_ERROR');
              }
              continue;
            }
            final value = jsonDecode(utf8.decode(line));
            line.clear();
            if (value is! Map<String, dynamic> ||
                value['v'] != 1 ||
                value['id'] != id ||
                value['ok'] is! bool ||
                finalReply != null ||
                value.keys.any(
                  (k) => !const {
                    'v',
                    'id',
                    'ok',
                    'error',
                    'event',
                    'snapshot',
                    'startupPolicy',
                  }.contains(k),
                )) {
              throw const UpdateException('UPDATE_PROTOCOL_ERROR');
            }
            if (value['event'] != null) {
              if (value['event'] != 'progress' ||
                  value['ok'] != true ||
                  op != 'download' ||
                  value.containsKey('startupPolicy') ||
                  value['error'] != null) {
                throw const UpdateException('UPDATE_PROTOCOL_ERROR');
              }
              final snapshot = UpdateSnapshot._decode(value['snapshot']);
              if (snapshot.state != UpdateState.downloading ||
                  snapshot.candidateId != fields['candidateId']) {
                throw const UpdateException('UPDATE_PROTOCOL_ERROR');
              }
              if (!aborted && !_disposed && !_progress.isClosed) {
                _progress.add(snapshot);
              }
            } else {
              finalReply = value;
            }
          }
        }
        final code = await process.exitCode;
        if (_cancelled || _disposed) {
          throw const UpdateException('UPDATE_CANCELLED');
        }
        if (stderrOverflow || line.isNotEmpty || finalReply == null) {
          throw const UpdateException('UPDATE_PROTOCOL_ERROR');
        }
        if (code != 0) throw const UpdateException('UPDATE_HELPER_FAILED');
        if (finalReply['ok'] != true) {
          if (finalReply.containsKey('snapshot') ||
              finalReply.containsKey('startupPolicy') ||
              finalReply['error'] is! String) {
            throw const UpdateException('UPDATE_PROTOCOL_ERROR');
          }
          final code = finalReply['error'] as String;
          throw UpdateException(
            _safeErrors.contains(code) ? code : 'UPDATE_FAILED',
          );
        }
        if (finalReply['error'] != null) {
          throw const UpdateException('UPDATE_PROTOCOL_ERROR');
        }
        return decode(finalReply);
      }

      final result = await exchange().timeout(timeout);
      finished = true;
      return result;
    } on TimeoutException {
      throw UpdateException(
        op == 'launch' ? 'UPDATE_LAUNCH_OUTCOME_UNKNOWN' : 'UPDATE_TIMEOUT',
      );
    } on UpdateException catch (error) {
      if (error.code == 'UPDATE_CANCELLED' && op == 'launch') {
        throw const UpdateException('UPDATE_LAUNCH_OUTCOME_UNKNOWN');
      }
      rethrow;
    } on FormatException {
      throw const UpdateException('UPDATE_PROTOCOL_ERROR');
    } catch (_) {
      throw const UpdateException('UPDATE_HELPER_FAILED');
    } finally {
      aborted = true;
      if (!finished && child != null) {
        child.kill();
        try {
          await child.exitCode.timeout(const Duration(seconds: 2));
        } catch (_) {
          /* Only this exact child. */
        }
      }
      await stderr?.cancel();
      _process = null;
      _busy = false;
    }
  }

  /// Download cancellation preserves resumable bytes; UI owns manual resumption.
  void cancel() {
    _cancelled = true;
    _process?.kill();
  }

  Future<void> dispose() async {
    _disposed = true;
    cancel();
    if (!_progress.isClosed) unawaited(_progress.close());
  }
}

const _safeErrors = {
  'UPDATE_UNCONFIGURED',
  'UPDATE_RELEASE_INVALID',
  'UPDATE_INVALID_REQUEST',
  'UPDATE_STATE_INVALID',
  'UPDATE_BUSY',
  'UPDATE_TRUST_FAILED',
  'UPDATE_CHECK_FAILED',
  'UPDATE_CANDIDATE_INVALID',
  'UPDATE_WAITING_NETWORK',
  'UPDATE_INSTALL_CONSENT_REQUIRED',
  'UPDATE_DOWNLOAD_FAILED',
  'UPDATE_PACKAGE_INVALID',
  'UPDATE_LAUNCH_FAILED',
  'UPDATE_LAUNCH_CANCELLED',
  'UPDATE_FAILED',
  'UPDATE_CONTROL_DNS_FAILED',
};

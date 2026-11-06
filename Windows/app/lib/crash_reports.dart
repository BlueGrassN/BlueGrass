import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter/foundation.dart';

import 'platform_store.dart';
import 'app_version.dart';
import 'desktop_platform.dart';

enum CrashModule { ui, nativeBroker, service, engine }

enum CrashCode {
  uiUnhandled('UI_UNHANDLED'),
  nativeCrash('NATIVE_CRASH'),
  serviceCrash('SERVICE_CRASH'),
  enginePanic('ENGINE_PANIC');

  const CrashCode(this.wire);
  final String wire;
}

enum CrashFrame {
  uiDispatch('ui.dispatch'),
  platformDispatch('ui.platformDispatch'),
  nativeReadLoop('native.readLoop'),
  serviceRun('service.run'),
  engineRun('engine.run');

  const CrashFrame(this.wire);
  final String wire;
}

/// A deliberate crash signal, never a raw Exception/StackTrace or business event.
class CrashSignal {
  const CrashSignal({
    required this.module,
    required this.code,
    this.frames = const [],
  });
  final CrashModule module;
  final CrashCode code;
  final List<CrashFrame> frames;
}

abstract class CrashReportStore {
  Future<String?> read();
  Future<void> write(String value);
  Future<void> delete();
}

class WindowsCrashReportStore implements CrashReportStore {
  static const key = 'crash_reports_v1';
  @override
  Future<String?> read() =>
      platform.invokeMethod<String>('readRecord', {'key': key});
  @override
  Future<void> write(String value) =>
      platform.invokeMethod<void>('writeRecord', {'key': key, 'value': value});
  @override
  Future<void> delete() =>
      platform.invokeMethod<void>('deleteRecord', {'key': key});
}

/// A future configured implementation must use the trusted control-plane route,
/// HTTPS, reportId idempotency, bounded responses and no business authentication.
/// This boundary never accepts a user-supplied URL. Production has no receiver.
abstract class CrashReportTransport {
  Uri? get receiver;
  Future<bool> send(Map<String, Object?> report);
  void cancel();
}

class UnconfiguredCrashReportTransport implements CrashReportTransport {
  @override
  Uri? get receiver => null;
  @override
  Future<bool> send(Map<String, Object?> report) async => false;
  @override
  void cancel() {}
}

/// Owns opt-in consent and an already-sanitized DPAPI queue for the OS user.
/// Limits are fixed release policy, never inputs from remote/UI error payloads.
class CrashReportCoordinator extends ChangeNotifier {
  CrashReportCoordinator({
    CrashReportStore? store,
    CrashReportTransport? transport,
    DateTime Function()? now,
    String Function()? nextId,
  }) : store = store ?? WindowsCrashReportStore(),
       transport = transport ?? UnconfiguredCrashReportTransport(),
       _now = now ?? (() => DateTime.now().toUtc()),
       _nextId = nextId ?? _randomId;
  final CrashReportStore store;
  final CrashReportTransport transport;
  final DateTime Function() _now;
  final String Function() _nextId;
  static const maxReports = 20,
      maxReportBytes = 2048,
      maxRecordBytes = 64 * 1024;
  static const retention = Duration(days: 7),
      sendTimeout = Duration(seconds: 15);
  static const maxAttempts = 3;
  static const retryDelays = [
    Duration(minutes: 1),
    Duration(minutes: 5),
    Duration(minutes: 30),
  ];
  static const appVersion = clientVersionLabel, coreVersion = 'xray/26.10.17';
  final List<_QueuedCrash> _queue = [];
  Future<void> _ordered = Future.value();
  Timer? _retry, _retentionTimer;
  bool _enabled = false,
      _initialized = false,
      _disposed = false,
      _sending = false;
  int _generation = 0;
  String? errorCode;
  bool get enabled => _enabled;
  bool get initialized => _initialized;
  int get count => _queue.length;
  bool get sending => _sending;
  bool get configured =>
      transport.receiver?.scheme == 'https' &&
      transport.receiver!.host.isNotEmpty &&
      transport.receiver!.userInfo.isEmpty;
  String get receiverLabel =>
      configured ? transport.receiver!.host : '未配置接收端（CONFIG_EMPTY）';

  static String _randomId() {
    final random = Random.secure();
    return List.generate(
      16,
      (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
  }

  Future<T> _serial<T>(Future<T> Function() operation) {
    final result = _ordered.then((_) => operation());
    _ordered = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<void> initialize() async {
    if (_disposed || initialized) return;
    await _serial(() async {
      if (_disposed || initialized) return;
      try {
        final raw = await store.read();
        if (_disposed) return;
        if (raw != null) {
          if (utf8.encode(raw).length > maxRecordBytes) {
            throw const FormatException();
          }
          final value = jsonDecode(raw);
          if (value is! Map<String, dynamic> ||
              value.length != 3 ||
              value['schema'] != 1 ||
              value['enabled'] is! bool ||
              value['queue'] is! List ||
              (value['queue'] as List).length > maxReports) {
            throw const FormatException();
          }
          final decoded = (value['queue'] as List)
              .map(_QueuedCrash.decode)
              .toList();
          if (decoded.map((e) => e.report['reportId']).toSet().length !=
                  decoded.length ||
              (value['enabled'] == false && decoded.isNotEmpty)) {
            throw const FormatException();
          }
          _enabled = value['enabled'] as bool;
          _queue.addAll(decoded);
          if (_prune()) await _persist();
        }
      } catch (_) {
        _enabled = false;
        _queue.clear();
        errorCode = 'CRASH_STORAGE_INVALID';
      }
      _initialized = true;
      _changed();
    });
    if (enabled) unawaited(sendPending());
  }

  Future<bool> setEnabled(bool value) async {
    if (_disposed || !initialized) return false;
    if (!value) {
      // Revoke synchronously so queued writes/new events cannot race consent.
      _enabled = false;
      _generation++;
      _retry?.cancel();
      transport.cancel();
      _queue.clear();
      _changed();
    }
    final generation = _generation;
    return _serial(() async {
      if (_disposed || generation != _generation) return false;
      try {
        await _persist(consent: value);
        if (!_disposed && generation == _generation) {
          _enabled = value;
          errorCode = null;
          _changed();
        }
        return true;
      } catch (_) {
        if (!value) {
          try {
            await store.delete();
            errorCode = null;
            _changed();
            return true;
          } catch (_) {}
        }
        errorCode = 'CRASH_STORAGE_FAILED';
        _changed();
        return false;
      }
    });
  }

  Future<void> record(CrashSignal signal) async {
    // No retrospective collection, including a callback queued before consent.
    if (_disposed || !initialized || !enabled) return;
    final generation = _generation;
    final frames = List<CrashFrame>.of(signal.frames.take(16));
    await _serial(() async {
      if (_disposed || !enabled || generation != _generation) return;
      try {
        final os =
            RegExp(r'\b\d{1,5}\.\d{1,5}(?:\.\d{1,5})?\b')
                .firstMatch(Platform.operatingSystemVersion)
                ?.group(0) ??
            'unknown';
        final report = <String, Object?>{
          'schema': 1,
          'reportId': _nextId(),
          'appVersion': appVersion,
          'coreVersion': coreVersion,
          'platform': DesktopPlatform.current.isMac ? 'macos' : 'windows-x64',
          'osVersion': os,
          'module': signal.module.name,
          'code': signal.code.wire,
          'frames': frames.map((f) => f.wire).toList(),
          'reportedAtUtc': _now().toUtc().toIso8601String(),
        };
        _validateReport(report);
        _prune();
        _queue.add(_QueuedCrash(report, 0, _now().toUtc()));
        while (_queue.length > maxReports) {
          _queue.removeAt(0);
        }
        await _persist();
        errorCode = configured ? null : 'CONFIG_EMPTY';
      } catch (_) {
        errorCode = 'CRASH_STORAGE_FAILED';
      }
      _changed();
    });
    if (!_disposed && enabled && configured) unawaited(sendPending());
  }

  bool _prune() {
    final before = _queue.length, now = _now().toUtc();
    _queue.removeWhere((item) {
      final time = DateTime.parse(item.report['reportedAtUtc'] as String);
      return now.difference(time) >= retention ||
          time.isAfter(now.add(const Duration(minutes: 5)));
    });
    return before != _queue.length;
  }

  Future<void> _persist({bool? consent}) {
    final value = jsonEncode({
      'schema': 1,
      'enabled': consent ?? enabled,
      'queue': (consent ?? enabled) ? _queue.map((e) => e.json()).toList() : [],
    });
    if (utf8.encode(value).length > maxRecordBytes) {
      throw const FormatException();
    }
    return store.write(value);
  }

  Future<String> exportJson() => _serial(() async {
    if (_prune() && initialized) {
      try {
        await _persist();
      } catch (_) {
        errorCode = 'CRASH_STORAGE_FAILED';
      }
      _changed();
    }
    return const JsonEncoder.withIndent('  ').convert({
      'schema': 1,
      'reports': _queue.map((item) => item.report).toList(),
    });
  });

  Future<void> sendPending() async {
    if (_disposed || !initialized || !enabled || _sending) return;
    if (!configured) {
      errorCode = 'CONFIG_EMPTY';
      _changed();
      return;
    }
    _sending = true;
    _retry?.cancel();
    _changed();
    final generation = _generation;
    try {
      for (
        var i = 0;
        i < maxReports && !_disposed && enabled && generation == _generation;
        i++
      ) {
        final entry = await _serial(() async {
          if (_disposed || !enabled || generation != _generation) return null;
          final pruned = _prune();
          final eligible = _queue
              .where((e) => e.attempts < maxAttempts && !e.next.isAfter(_now()))
              .firstOrNull;
          if (eligible == null) {
            if (pruned) await _persist();
            return null;
          }
          eligible.next = _now().toUtc().add(retryDelays[eligible.attempts]);
          eligible.attempts++;
          await _persist(); // Bound retries across restart before sending.
          return eligible;
        });
        if (entry == null ||
            _disposed ||
            !enabled ||
            generation != _generation) {
          break;
        }
        var accepted = false;
        try {
          // A detached copy prevents a transport from mutating queued content.
          final copy = Map<String, Object?>.from(
            jsonDecode(jsonEncode(entry.report)) as Map,
          );
          accepted = await transport
              .send(copy)
              .timeout(
                sendTimeout,
                onTimeout: () {
                  transport.cancel();
                  return false;
                },
              );
        } catch (_) {
          /* No exception text enters storage or diagnostics. */
        }
        if (_disposed || !enabled || generation != _generation) break;
        await _serial(() async {
          if (_disposed || !enabled || generation != _generation) return;
          if (accepted) _queue.remove(entry);
          errorCode = accepted ? null : 'CRASH_SEND_FAILED';
          await _persist();
          _changed();
        });
      }
    } catch (_) {
      if (!_disposed) errorCode = 'CRASH_STORAGE_FAILED';
    } finally {
      _sending = false;
      _scheduleRetry();
      _changed();
    }
  }

  void _scheduleRetry() {
    _retry?.cancel();
    if (_disposed || !enabled || !configured) return;
    final eligible = _queue.where((e) => e.attempts < maxAttempts).toList()
      ..sort((a, b) => a.next.compareTo(b.next));
    if (eligible.isEmpty) return;
    final delay = eligible.first.next.difference(_now());
    // Storage/network faults must not produce a tight callback loop.
    _retry = Timer(
      delay < const Duration(minutes: 1) ? const Duration(minutes: 1) : delay,
      () => unawaited(sendPending()),
    );
  }

  void _scheduleRetention() {
    _retentionTimer?.cancel();
    if (_disposed || !enabled || _queue.isEmpty) return;
    final oldest = _queue
        .map((e) => DateTime.parse(e.report['reportedAtUtc'] as String))
        .reduce((a, b) => a.isBefore(b) ? a : b);
    final remaining = oldest.add(retention).difference(_now());
    _retentionTimer = Timer(
      remaining > Duration.zero ? remaining : const Duration(seconds: 1),
      () {
        unawaited(
          _serial(() async {
            if (_disposed || !enabled) return;
            try {
              if (_prune()) await _persist();
            } catch (_) {
              errorCode = 'CRASH_STORAGE_FAILED';
            }
            _changed();
          }),
        );
      },
    );
  }

  void _changed() {
    if (!_disposed) {
      _scheduleRetention();
      notifyListeners();
    }
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _generation++;
    _retry?.cancel();
    _retentionTimer?.cancel();
    transport.cancel();
    super.dispose();
  }
}

void _validateReport(Map<String, Object?> value) {
  const keys = {
    'schema',
    'reportId',
    'appVersion',
    'coreVersion',
    'platform',
    'osVersion',
    'module',
    'code',
    'frames',
    'reportedAtUtc',
  };
  final time = value['reportedAtUtc'] is String
      ? DateTime.tryParse(value['reportedAtUtc'] as String)
      : null;
  if (value.keys.toSet().difference(keys).isNotEmpty ||
      value.length != keys.length ||
      value['schema'] != 1 ||
      value['reportId'] is! String ||
      !RegExp(r'^[a-f0-9]{32}$').hasMatch(value['reportId'] as String) ||
      value['appVersion'] is! String ||
      !RegExp(
        r'^\d{1,5}\.\d{1,5}\.\d{1,5}(?:-(?:beta|rc)\.\d{1,5})?(?:\+\d{1,5})?$',
      ).hasMatch(value['appVersion'] as String) ||
      value['coreVersion'] is! String ||
      !RegExp(r'^(?:xray|sing-box)/\d{1,4}\.\d{1,4}\.\d{1,4}$')
          .hasMatch(value['coreVersion'] as String) ||
      !const {'windows-x64', 'macos'}.contains(value['platform']) ||
      value['osVersion'] is! String ||
      !RegExp(r'^(unknown|\d{1,5}\.\d{1,5}(?:\.\d{1,5})?)$')
          .hasMatch(value['osVersion'] as String) ||
      !CrashModule.values.any((m) => m.name == value['module']) ||
      !CrashCode.values.any((c) => c.wire == value['code']) ||
      value['frames'] is! List ||
      (value['frames'] as List).length > 16 ||
      !(value['frames'] as List).every(
        (v) => CrashFrame.values.any((f) => f.wire == v),
      ) ||
      time == null ||
      !time.isUtc ||
      time.toIso8601String() != value['reportedAtUtc'] ||
      utf8.encode(jsonEncode(value)).length >
          CrashReportCoordinator.maxReportBytes) {
    throw const FormatException('Invalid crash report schema');
  }
}

class _QueuedCrash {
  _QueuedCrash(this.report, this.attempts, this.next);
  final Map<String, Object?> report;
  int attempts;
  DateTime next;
  Map<String, Object?> json() => {
    'report': report,
    'attempts': attempts,
    'nextUtc': next.toUtc().toIso8601String(),
  };
  static _QueuedCrash decode(Object? value) {
    if (value is! Map<String, dynamic> ||
        value.length != 3 ||
        value['report'] is! Map ||
        value['attempts'] is! int ||
        (value['attempts'] as int) < 0 ||
        (value['attempts'] as int) > CrashReportCoordinator.maxAttempts ||
        value['nextUtc'] is! String) {
      throw const FormatException();
    }
    final report = Map<String, Object?>.from(value['report'] as Map);
    _validateReport(report);
    final next = DateTime.parse(value['nextUtc'] as String);
    if (!next.isUtc || next.toIso8601String() != value['nextUtc']) {
      throw const FormatException();
    }
    return _QueuedCrash(report, value['attempts'] as int, next);
  }
}

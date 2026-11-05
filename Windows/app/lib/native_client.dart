import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'desktop_platform.dart';

/// Starts only a private child. Production uses Process.start(runInShell: false).
typedef NativeProcessStarter = Future<Process> Function(
  String executable,
  List<String> arguments,
);

/// Transport failure; neither process death nor a timeout proves restoration.
class NativeClientException implements Exception {
  const NativeClientException(this.code, {this.recoveryUnknown = true});
  final String code;
  final bool recoveryUnknown;
  @override
  String toString() => 'NativeClientException($code)';
}

/// Private JSON-lines IPC to BlueGrass.Native.exe beside the running app.
///
/// Wire request: {id, command, profile?, mode?, allowLan?}; Hello is requested by
/// start(). Replies: {id, ok, error?, snapshot?, runtimeEpoch?, capabilities?}.
/// Status events use {event: status|snapshot, snapshot}. Transport failure emits
/// {event: failure, error: fixedCode, recoveryUnknown: true}. No secrets enter
/// argv or logs. Failed backend replies keep ok=false; transport failures throw
/// NativeClientException. Unknown backend error text becomes NATIVE_REJECTED.
///
/// Calls are serialized, including GetStatus. A transport failure is terminal;
/// create a new client to recover. stop() requires an acknowledged Disconnect
/// and a clean child exit. Forced termination never counts as restoration.
/// Hello binds this child's runtime. Successful Connect/SwitchResource replies
/// replace the bound context. Only a matching failed SwitchResource with the
/// explicit committed-journal recovery marker may clear it while protection
/// remains active; other replies and events retain the exact bound identity.
class NativeClient {
  NativeClient({
    NativeProcessStarter? startProcess,
    Duration? requestTimeout,
    this.shutdownTimeout = const Duration(seconds: 5),
  }) : _startProcess = startProcess ?? _spawn,
       // Leave room for bounded macOS proxy transactions and their cleanup;
       // the backend owns helper termination and recovery within this deadline.
       requestTimeout =
           requestTimeout ??
           Duration(seconds: DesktopPlatform.current.isMac ? 90 : 45) {
    if (this.requestTimeout <= Duration.zero ||
        shutdownTimeout <= Duration.zero) {
      throw ArgumentError('IPC timeouts must be positive');
    }
  }

  static const maxMessageBytes = 64 * 1024;
  static const _keyFields = {
    'environment',
    'dataSource',
    'accountKey',
    'kind',
    'sourceId',
  };
  static const _contextIds = {
    'runtimeEpoch',
    'clientEpoch',
    'logicalSessionId',
    'attemptId',
  };
  static const _commands = {
    'Hello',
    'GetStatus',
    'Connect',
    'SwitchResource',
    'Pause',
    'Disconnect',
    'ReleaseProtection',
    'ApplyResourceUpdate',
    'ResolveControlHost',
    'Diagnose',
  };
  // Never surface arbitrary native stderr/error strings: they may contain a
  // failed request or node credentials. Unknown codes have one safe fallback.
  static const _errorCodes = {
    'CANCELLED',
    'CLOSED',
    'ENGINE_FAILURE',
    'HISTORY_FULL',
    'INVALID_CLOCK',
    'INVALID_ENTRY_DNS',
    'CONTROL_DNS_FAILED',
    'INVALID_CONFIGURATION',
    'INVALID_CONTEXT',
    'INVALID_MODE',
    'INVALID_PARAMETERS',
    'INVALID_REQUEST',
    'OWNER_MISMATCH',
    'PROFILE_CONTEXT_MISMATCH',
    'REQUEST_CONFLICT',
    'RETIRED_ATTEMPT',
    'SESSION_RESOURCE_MISMATCH',
    'STALE_CONTEXT',
    'STALE_RESOURCE_REVISION',
    'STALE_SEQUENCE',
    'UNSUPPORTED_COMMAND',
    'OWNERSHIP_LOST',
    'RECOVERY_REQUIRED',
    'SERVICE_UNAVAILABLE',
    'SERVICE_CALLER_REJECTED',
    'SERVICE_BUSY',
    'DISCONNECT_REQUIRED',
    'EXIT_MISMATCH',
    'PROBE_FAILED',
    'EXPIRED',
    'NATIVE_REJECTED',
    'DIAGNOSTIC_BUSY',
    'DIAGNOSTIC_UNAVAILABLE',
    'DIAGNOSTIC_CANCELLED',
    'DIAGNOSTIC_TIMEOUT',
    'DIAGNOSTIC_FAILED',
    'DIAGNOSTIC_INVALID_REPORT',
  };

  final NativeProcessStarter _startProcess;
  final Duration requestTimeout;
  final Duration shutdownTimeout;
  final _random = Random.secure();
  final _events = StreamController<Map<String, dynamic>>.broadcast();
  final _line = <int>[];
  Process? _process;
  StreamSubscription<List<int>>? _stdout, _stderr;
  Future<Map<String, dynamic>>? _starting;
  Future<void> _queue = Future.value();
  Future<void>? _stopped, _cleanup;
  _PendingRequest? _pending;
  NativeClientException? _failure;
  String? _runtimeEpoch;
  Map<String, dynamic>? _context;
  bool _ready = false, _stopping = false, _disconnectConfirmed = false;
  bool _detachedForUpdate = false;
  bool externalProxyPreserved = false;
  String _mode = 'system_proxy';

  static Future<Process> _spawn(String executable, List<String> arguments) =>
      Process.start(executable, arguments, runInShell: false);

  bool get available => _ready && !_stopping && _failure == null;
  Stream<Map<String, dynamic>> get events => _events.stream;

  Future<Map<String, dynamic>> start({String? mode}) {
    if (mode == 'tun' && !DesktopPlatform.current.supportsTun) {
      return Future.error(const NativeClientException('UNSUPPORTED_COMMAND'));
    }
    if (mode != null) {
      if (!const {'system_proxy', 'tun'}.contains(mode) ||
          (_starting != null && mode != _mode)) {
        return Future.error(const NativeClientException('INVALID_MODE'));
      }
      _mode = mode;
    }
    if (_failure != null) return Future.error(_failure!);
    if (_stopping) {
      return Future.error(const NativeClientException('NATIVE_CLOSED'));
    }
    return _starting ??= _launch();
  }

  Future<Map<String, dynamic>> _launch() async {
    final executable = DesktopPlatform.current.helperPath('BlueGrass.Native');
    try {
      await _startProcess(executable, [
            _mode == 'tun' ? '--tun' : '--system-proxy',
          ])
          .then((process) {
            _process = process;
            _stdout = process.stdout.listen(
              _receive,
              onError: (Object _) {
                _fail('NATIVE_IO_ERROR');
              },
              onDone: () {
                if (_line.isNotEmpty) {
                  _fail('NATIVE_PROTOCOL_ERROR');
                } else if (!_disconnectConfirmed && !_detachedForUpdate) {
                  _fail('NATIVE_EXITED');
                }
              },
            );
            _stderr = process.stderr.listen((_) {}, onError: (Object _) {});
            unawaited(
              process.stdin.done.then<void>(
                (_) {},
                onError: (Object _) {
                  _fail('NATIVE_IO_ERROR');
                },
              ),
            );
            unawaited(
              process.exitCode.then<void>((code) {
                if (code != 0 &&
                    !_stopping &&
                    !_detachedForUpdate &&
                    (_failure == null || _failure!.code == 'NATIVE_EXITED') &&
                    !_events.isClosed) {
                  _events.add({'event': 'crash', 'error': 'NATIVE_EXITED'});
                }
                if ((!_disconnectConfirmed && !_detachedForUpdate) ||
                    code != 0) {
                  _fail('NATIVE_EXITED');
                }
              }, onError: (Object _) => _fail('NATIVE_IO_ERROR')),
            );
            // Process creation can finish after its deadline. Still own and clean
            // that exact child rather than leaving a detached native process.
            if (_failure != null) _cleanup = _cleanupProcess();
          })
          .timeout(requestTimeout);
    } catch (_) {
      _fail('NATIVE_START_FAILED');
      throw _failure!;
    }
    if (_failure != null) throw _failure!;
    final hello = await _exchange(_encode('Hello'));
    if (hello['ok'] != true) {
      _fail(hello['error'] as String);
      throw _failure!;
    }
    _ready = true;
    return hello;
  }

  Future<Map<String, dynamic>> request(
    String command, {
    Map<String, dynamic>? profile,
    String? mode,
    bool? allowLan,
    DateTime? serverTimeUtc,
    Map<String, dynamic>? entryDns,
    String? host,
    Map<String, dynamic>? update,
  }) {
    if (mode == 'tun' && !DesktopPlatform.current.supportsTun) {
      return Future.error(const NativeClientException('UNSUPPORTED_COMMAND'));
    }
    if (_failure != null) return Future.error(_failure!);
    if (_stopping) {
      return Future.error(const NativeClientException('NATIVE_CLOSED'));
    }
    late final _WireRequest request;
    try {
      // Encode at submission, not dequeue time, to freeze nested credentials.
      request = _encode(
        command,
        profile: profile,
        mode: mode,
        allowLan: allowLan,
        serverTimeUtc: serverTimeUtc,
        entryDns: entryDns,
        host: host,
        update: update,
      );
    } on NativeClientException catch (error) {
      return Future.error(error);
    }
    final starting = start();
    return _enqueue(() async {
      await starting;
      if (_failure != null) throw _failure!;
      return _exchange(request);
    });
  }

  Future<Map<String, dynamic>> status() => request('GetStatus');

  Future<T> _enqueue<T>(Future<T> Function() operation) {
    final result = _queue.then((_) => operation());
    _queue = result.then<void>((_) {}, onError: (Object _) {});
    return result;
  }

  _WireRequest _encode(
    String command, {
    Map<String, dynamic>? profile,
    String? mode,
    bool? allowLan,
    DateTime? serverTimeUtc,
    Map<String, dynamic>? entryDns,
    String? host,
    Map<String, dynamic>? update,
  }) {
    if (!_commands.contains(command) ||
        const {'Diagnose', 'SwitchResource'}.contains(command) &&
            _context == null) {
      throw const NativeClientException(
        'NATIVE_INVALID_REQUEST',
        recoveryUnknown: false,
      );
    }
    final bytes = List<int>.generate(16, (_) => _random.nextInt(256));
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    final hex = bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
    final id =
        '${hex.substring(0, 8)}-${hex.substring(8, 12)}-'
        '${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
    late final List<int> encoded;
    try {
      encoded = utf8.encode(
        jsonEncode({
          'id': id,
          'command': command,
          if (const {'Diagnose', 'SwitchResource'}.contains(command))
            'context': _context,
          'profile': ?profile,
          'mode': ?mode,
          'allowLan': ?allowLan,
          'serverTimeUtc': ?serverTimeUtc?.toUtc().toIso8601String(),
          'entryDns': ?entryDns,
          'host': ?host,
          'update': ?update,
        }),
      );
    } catch (_) {
      throw const NativeClientException(
        'NATIVE_INVALID_REQUEST',
        recoveryUnknown: false,
      );
    }
    if (encoded.length > maxMessageBytes) {
      throw const NativeClientException(
        'NATIVE_MESSAGE_TOO_LARGE',
        recoveryUnknown: false,
      );
    }
    // Read the same frozen JSON the child will see, never the caller's mutable
    // profile. Keep only the resource identity for response validation.
    final wire = jsonDecode(utf8.decode(encoded)) as Map<String, dynamic>;
    final wireProfile = wire['profile'];
    final key = wireProfile is Map ? _resourceKey(wireProfile['key']) : null;
    return _WireRequest(
      id,
      [...encoded, 10],
      command,
      key,
      _readContext(wire['context']),
    );
  }

  Future<Map<String, dynamic>> _exchange(_WireRequest request) async {
    if (_failure != null) throw _failure!;
    final pending = _PendingRequest(request);
    _pending = pending;
    final timer = Timer(requestTimeout, () => _fail('NATIVE_TIMEOUT'));
    try {
      _process!.stdin.add(request.bytes);
      unawaited(
        _process!.stdin.flush().catchError((Object _) {
          _fail('NATIVE_IO_ERROR');
        }),
      );
      final response = await pending.result.future;
      // Another frame in the same chunk can invalidate this reply before its
      // future resumes (for example a duplicate or a malformed trailing frame).
      if (_failure != null) throw _failure!;
      return response;
    } catch (_) {
      if (_failure == null) _fail('NATIVE_IO_ERROR');
      throw _failure!;
    } finally {
      timer.cancel();
      if (identical(_pending, pending)) _pending = null;
    }
  }

  void _receive(List<int> bytes) {
    if (_failure != null) return;
    for (final byte in bytes) {
      if (byte == 10) {
        try {
          final decoded = jsonDecode(utf8.decode(_line));
          _line.clear();
          if (decoded is! Map<String, dynamic>) {
            _fail('NATIVE_PROTOCOL_ERROR');
          } else {
            _message(decoded);
          }
        } catch (_) {
          _fail('NATIVE_PROTOCOL_ERROR');
        }
      } else {
        if (_line.length >= maxMessageBytes) {
          _fail('NATIVE_MESSAGE_TOO_LARGE');
        } else {
          _line.add(byte);
        }
      }
      if (_failure != null) return;
    }
  }

  void _message(Map<String, dynamic> message) {
    if (!message.containsKey('id') &&
        const {'status', 'snapshot'}.contains(message['event']) &&
        message['snapshot'] is Map<String, dynamic>) {
      if (!_acceptIdentity(message)) {
        _fail('NATIVE_CONTEXT_MISMATCH');
        return;
      }
      _events.add({'event': message['event'], 'snapshot': message['snapshot']});
      return;
    }
    final pending = _pending;
    if (pending == null ||
        message['id'] != pending.id ||
        message['ok'] is! bool ||
        (message['ok'] == true &&
            message['snapshot'] is! Map<String, dynamic>) ||
        (message.containsKey('snapshot') &&
            message['snapshot'] is! Map<String, dynamic>)) {
      _fail('NATIVE_PROTOCOL_ERROR');
      return;
    }
    if (!_acceptIdentity(message, request: pending.request)) {
      _fail('NATIVE_CONTEXT_MISMATCH');
      return;
    }
    final response = <String, dynamic>{
      'id': pending.id,
      'ok': message['ok'],
      if (message['snapshot'] != null) 'snapshot': message['snapshot'],
      if (message['runtimeEpoch'] is String)
        'runtimeEpoch': message['runtimeEpoch'],
      if (message['capabilities'] is List)
        'capabilities': message['capabilities'],
      if (message['ok'] == false)
        'error': _errorCodes.contains(message['error'])
            ? message['error']
            : 'NATIVE_REJECTED',
    };
    _pending = null;
    pending.result.complete(response);
  }

  static bool _identifier(Object? value) =>
      value is String && value.isNotEmpty && value.trim() == value;

  static Map<String, String>? _resourceKey(Object? value) {
    if (value is! Map ||
        value.length != _keyFields.length ||
        !_keyFields.every((key) => _identifier(value[key]))) {
      return null;
    }
    return Map.unmodifiable({
      for (final key in _keyFields) key: value[key] as String,
    });
  }

  static bool _sameKey(Map<String, String> a, Map<String, String> b) =>
      _keyFields.every((key) => a[key] == b[key]);

  static Map<String, dynamic>? _readContext(Object? value) {
    if (value is! Map ||
        value.length != 6 ||
        value['schemaVersion'] is! int ||
        value['schemaVersion'] != 1 ||
        !_contextIds.every((key) => _identifier(value[key]))) {
      return null;
    }
    final key = _resourceKey(value['resourceKey']);
    if (key == null) return null;
    // Detach every retained field from the map exposed to the UI/event stream.
    return Map.unmodifiable({
      'schemaVersion': 1,
      'resourceKey': key,
      for (final id in _contextIds) id: value[id] as String,
    });
  }

  static bool _idleWithoutController(Map<String, dynamic> snapshot) =>
      snapshot['context'] == null &&
      snapshot['connection'] == 'disconnected' &&
      snapshot['actualMode'] == 'none' &&
      snapshot['gateKnown'] == true &&
      snapshot['businessGateOpen'] == false &&
      snapshot['protectionIntent'] != true &&
      snapshot['systemProxyApplied'] == false;

  static bool _protectedWithoutController(Map<String, dynamic> snapshot) =>
      snapshot['context'] == null &&
      snapshot['connection'] == 'paused' &&
      snapshot['actualMode'] == 'tun' &&
      snapshot['gateKnown'] == true &&
      snapshot['businessGateOpen'] == false &&
      snapshot['protectionIntent'] == true &&
      snapshot['protection'] == 'wfp_blocking' &&
      snapshot['systemProxyApplied'] == false;

  static bool _sameContext(Map<String, dynamic> a, Map<String, dynamic> b) =>
      _contextIds.every((key) => a[key] == b[key]) &&
      _sameKey(
        a['resourceKey'] as Map<String, String>,
        b['resourceKey'] as Map<String, String>,
      );

  // A committed resource switch can fail before the new controller adopts its
  // context. The authenticated host then retains WFP and returns this one-shot
  // acknowledgement. Missing context alone is never authority to forget a bound
  // attempt, and a request queued under an old context cannot retire a newer one.
  bool _acceptContextRecovery(
    Map<String, dynamic> message,
    Map<String, dynamic> snapshot,
    _WireRequest? request,
  ) {
    final current = _context;
    if (request?.command != 'SwitchResource' ||
        message['ok'] != false ||
        _runtimeEpoch == null ||
        snapshot['runtimeEpoch'] != _runtimeEpoch ||
        (message.containsKey('runtimeEpoch') &&
            message['runtimeEpoch'] != _runtimeEpoch) ||
        current == null ||
        request!.context == null ||
        request.resourceKey == null ||
        !_sameContext(request.context!, current) ||
        snapshot.containsKey('context') ||
        !_protectedWithoutController(snapshot) ||
        snapshot['proxyRecoveryRequired'] != false) {
      return false;
    }
    final marker = snapshot['contextRecovery'];
    const fields = {
      'version',
      'reason',
      'requestId',
      'previousContext',
      'resourceKey',
    };
    if (marker is! Map ||
        marker.length != fields.length ||
        !fields.every(marker.containsKey) ||
        marker['version'] is! int ||
        marker['version'] != 1 ||
        marker['reason'] != 'resource_switch_committed' ||
        marker['requestId'] != request.id) {
      return false;
    }
    final previous = _readContext(marker['previousContext']);
    final destination = _resourceKey(marker['resourceKey']);
    if (previous == null ||
        destination == null ||
        !_sameContext(previous, current) ||
        !_sameKey(destination, request.resourceKey!)) {
      return false;
    }
    final oldKey = current['resourceKey'] as Map<String, String>;
    if (_sameKey(oldKey, destination) ||
        !const {
          'environment',
          'dataSource',
          'accountKey',
        }.every((key) => oldKey[key] == destination[key])) {
      return false;
    }
    _context = null;
    return true;
  }

  bool _acceptIdentity(Map<String, dynamic> message, {_WireRequest? request}) {
    final snapshot = message['snapshot'];
    if (snapshot is Map<String, dynamic> &&
        snapshot.containsKey('contextRecovery')) {
      return _acceptContextRecovery(message, snapshot, request);
    }
    if (_runtimeEpoch == null) {
      if (request?.command != 'Hello') return false;
      // A refused initial Hello cannot make start() succeed or publish a
      // snapshot. Preserve its fixed error code without binding any identity.
      if (message['ok'] == false) return true;
      final runtime = message['runtimeEpoch'];
      if (!_identifier(runtime) ||
          snapshot is! Map<String, dynamic> ||
          snapshot['runtimeEpoch'] != runtime ||
          !(_idleWithoutController(snapshot) ||
              _protectedWithoutController(snapshot))) {
        return false;
      }
      _runtimeEpoch = runtime as String;
      return true;
    }
    if ((message.containsKey('runtimeEpoch') &&
            message['runtimeEpoch'] != _runtimeEpoch) ||
        (request?.command == 'Hello' &&
            message['ok'] == true &&
            message['runtimeEpoch'] != _runtimeEpoch)) {
      return false;
    }
    // A rejected command can carry just its error code, but any snapshot it
    // includes must belong to the current runtime and previously bound context.
    if (snapshot == null) return request != null && message['ok'] == false;
    if (snapshot is! Map<String, dynamic> ||
        snapshot['runtimeEpoch'] != _runtimeEpoch) {
      return false;
    }
    final next = _readContext(snapshot['context']);
    if (const {'Connect', 'SwitchResource'}.contains(request?.command) &&
        message['ok'] == true) {
      if (next == null ||
          next['runtimeEpoch'] != _runtimeEpoch ||
          request!.resourceKey == null ||
          !_sameKey(
            next['resourceKey'] as Map<String, String>,
            request.resourceKey!,
          )) {
        return false;
      }
      _context = next;
      return true;
    }
    final current = _context;
    if (current == null) {
      return _idleWithoutController(snapshot) ||
          _protectedWithoutController(snapshot);
    }
    return next != null && _sameContext(next, current);
  }

  void _fail(String code) {
    if (_failure != null) return;
    _failure = NativeClientException(code);
    _ready = false;
    _line.clear();
    final pending = _pending;
    _pending = null;
    pending?.result.completeError(_failure!);
    if (!_events.isClosed) {
      _events.add({'event': 'failure', 'error': code, 'recoveryUnknown': true});
    }
    _cleanup ??= _cleanupProcess();
  }

  /// Disconnect acknowledgement must confirm a known closed business gate.
  /// Closing stdin then asks the native host to perform its final cleanup.
  Future<void> detachForUpdate() {
    if (_stopping) {
      return Future.error(const NativeClientException('NATIVE_CLOSED'));
    }
    _stopping = true;
    return _enqueue(() async {
      try {
        if (_starting != null) await _starting;
        if (_failure != null) throw _failure!;
        if (_process != null) {
          final reply = await _exchange(_encode('Pause'));
          final snapshot = reply['snapshot'];
          if (reply['ok'] != true ||
              snapshot is! Map ||
              snapshot['gateKnown'] != true ||
              snapshot['businessGateOpen'] != false) {
            throw const NativeClientException('NATIVE_REJECTED');
          }
          _detachedForUpdate = true;
          await _process!.stdin.close().timeout(shutdownTimeout);
          final code = await _process!.exitCode.timeout(shutdownTimeout);
          if (code != 0 || _failure != null) {
            throw const NativeClientException('NATIVE_EXITED');
          }
        }
        _ready = false;
        await _closeStreams();
      } catch (_) {
        _fail('NATIVE_IO_ERROR');
        await _cleanup;
        throw _failure!;
      }
    });
  }

  Future<void> stop() {
    if (_stopped != null) return _stopped!;
    _stopping = true;
    return _stopped = _enqueue(() async {
      try {
        if (_starting != null) await _starting;
        if (_failure != null) throw _failure!;
        if (_process != null) {
          final reply = await _exchange(_encode('Disconnect'));
          if (reply['ok'] != true) {
            throw NativeClientException(reply['error'] as String);
          }
          final snapshot = reply['snapshot'];
          if (snapshot is! Map ||
              snapshot['businessGateOpen'] != false ||
              snapshot['gateKnown'] != true ||
              snapshot['systemProxyApplied'] != false ||
              snapshot['protectionIntent'] == true ||
              snapshot['proxyRecoveryRequired'] == true) {
            throw const NativeClientException('NATIVE_DISCONNECT_UNCONFIRMED');
          }
          _disconnectConfirmed = true;
          externalProxyPreserved = snapshot['proxySettingsPreserved'] == true;
          await _process!.stdin.close().timeout(shutdownTimeout);
          final code = await _process!.exitCode.timeout(shutdownTimeout);
          if (code != 0) throw const NativeClientException('NATIVE_EXITED');
          if (_failure != null) throw _failure!;
        }
        _ready = false;
        await _closeStreams();
      } on TimeoutException {
        _fail('NATIVE_SHUTDOWN_TIMEOUT');
        await _cleanup;
        throw _failure!;
      } catch (error) {
        _fail(error is NativeClientException ? error.code : 'NATIVE_IO_ERROR');
        await _cleanup;
        throw _failure!;
      }
    });
  }

  Future<void> _cleanupProcess() async {
    final process = _process;
    if (process != null) {
      try {
        await process.stdin.close().timeout(shutdownTimeout);
      } catch (_) {
        /* Transport already failed; no restoration claim. */
      }
      try {
        await process.exitCode.timeout(shutdownTimeout);
      } catch (_) {
        // Only our exact child, never a service or discovered PID.
        process.kill();
        try {
          await process.exitCode.timeout(shutdownTimeout);
        } catch (_) {
          /* Caller still receives recoveryUnknown. */
        }
      }
    }
    await _closeStreams();
  }

  Future<void> _closeStreams() async {
    await _stdout?.cancel();
    await _stderr?.cancel();
    // A paused UI subscriber must not hold the process lifecycle open.
    if (!_events.isClosed) unawaited(_events.close());
  }
}

class _WireRequest {
  const _WireRequest(
    this.id,
    this.bytes,
    this.command,
    this.resourceKey,
    this.context,
  );
  final String id;
  final List<int> bytes;
  final String command;
  final Map<String, String>? resourceKey;
  final Map<String, dynamic>? context;
}

class _PendingRequest {
  _PendingRequest(this.request);
  final _WireRequest request;
  String get id => request.id;
  final result = Completer<Map<String, dynamic>>();
}

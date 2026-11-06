// A real, isolated child process for NativeClient transport tests. It opens no
// sockets and does not inspect or change the host's network configuration.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

Future<void> main(List<String> args) async {
  final scenario = args.first;
  if (args.length == 2) {
    await Future<void>.delayed(Duration(milliseconds: int.parse(args[1])));
  }
  var requests = 0;
  var busy = false;
  var connects = 0;
  var statusReads = 0;
  var networkChanged = false;
  Map<String, dynamic>? diagnostic;
  Map<String, dynamic>? lastProfile;
  String? observedExit;
  var connectedStatusReads = 0;
  Map<String, dynamic>? context;
  Map<String, dynamic>? previousContext;
  var connection = scenario.endsWith('recovered-tun')
      ? 'paused'
      : 'disconnected';
  String? firstId;
  final commands = <String>[];
  final waiting = Completer<void>();

  void send(Map<String, dynamic> value) => stdout.writeln(jsonEncode(value));
  Map<String, dynamic> snapshot(Map<String, dynamic> request) => {
    'runtimeEpoch': 'fixture-runtime',
    if (context != null) 'context': jsonDecode(jsonEncode(context)),
    'connection': connection,
    'actualMode': scenario.endsWith('tun') && connection != 'disconnected'
        ? 'tun'
        : context == null || connection == 'disconnected'
        ? 'none'
        : scenario.endsWith('system-proxy')
        ? 'system_proxy'
        : 'local_proxy',
    'systemProxyApplied':
        scenario.endsWith('system-proxy') &&
        context != null &&
        connection != 'disconnected',
    'proxyRecoveryRequired': false,
    'protectionIntent':
        scenario.endsWith('tun') &&
        (connection != 'disconnected' ||
            scenario == 'disconnect-protected-tun' &&
                request['command'] == 'Disconnect'),
    'protection': scenario.startsWith('mac-')
        ? (connection == 'connected' ? 'pf_forwarding' : 'pf_blocking')
        : (connection == 'connected' ? 'wfp_forwarding' : 'wfp_blocking'),
    if (networkChanged) 'errorCode': 'NETWORK_CHANGED',
    'verifiedExitIpv4': connection == 'connected' ? observedExit : null,
    'proxyAddress': '127.0.0.1:49152',
    'businessGateOpen': connection == 'connected',
    'gateKnown': true,
    'sequence': requests,
    'commands': List<String>.of(commands),
    'received': request,
    'diagnostic': ?diagnostic,
    'lastProfile': ?lastProfile,
  };

  void poison(Map<String, dynamic> value, String defect) {
    switch (defect) {
      case 'runtime':
        value['runtimeEpoch'] = 'wrong-runtime';
      case 'missing-runtime':
        value.remove('runtimeEpoch');
      case 'context-runtime':
        value['context']['runtimeEpoch'] = 'wrong-runtime';
      case 'schema':
        value['context']['schemaVersion'] = 999;
      case 'client':
        value['context']['clientEpoch'] = 'foreign-client';
      case 'session':
        value['context']['logicalSessionId'] = 'foreign-session';
      case 'attempt':
        value['context']['attemptId'] = 'foreign-attempt';
      case 'empty-client':
        value['context']['clientEpoch'] = '';
      case 'empty-session':
        value['context']['logicalSessionId'] = '';
      case 'empty-attempt':
        value['context']['attemptId'] = '';
      case 'missing-context':
        value.remove('context');
      case 'old-attempt':
        value['context'] = previousContext;
      case 'mode':
        value['actualMode'] = 'local_proxy';
      case 'open':
        value['businessGateOpen'] = true;
      case 'proxy':
        value['systemProxyApplied'] = true;
      case 'unknown':
        value['gateKnown'] = false;
      case 'unexpected-context':
        value['context'] = {
          'schemaVersion': 999,
          'runtimeEpoch': 'wrong-runtime',
          'clientEpoch': 'foreign-client',
          'logicalSessionId': 'foreign-session',
          'attemptId': 'foreign-attempt',
          'resourceKey': {'accountKey': 'foreign-account'},
        };
      default:
        if (!defect.startsWith('key-')) throw StateError('Unknown scenario');
        value['context']['resourceKey'][defect.substring(4)] = 'foreign-value';
    }
  }

  Map<String, dynamic> reply(Map<String, dynamic> request) {
    if (request['command'] == 'Diagnose') {
      diagnostic = {
        'state': 'running',
        'generation': 1,
        'context': jsonDecode(jsonEncode(context)),
      };
      if (scenario == 'stale-diagnostics-tun') {
        diagnostic!['context']['attemptId'] = 'retired-attempt';
      }
    } else if (request['command'] == 'GetStatus' && diagnostic != null) {
      final probeFailed = scenario.startsWith('probe-failure');
      if (scenario.startsWith('diagnostic-change')) observedExit = '9.9.9.9';
      if (probeFailed) observedExit = null;
      diagnostic!['state'] = 'completed';
      diagnostic!['report'] = {
        'version': 1,
        'generation': 1,
        'cancelled': false,
        'results': [
          {
            'id': 'proxy_exit',
            'status': probeFailed ? 'failed' : 'passed',
            'code': probeFailed ? 'probe_failed' : 'observed_exit',
          },
          {
            'id': 'proxy_udp',
            'status': 'skipped',
            'code': 'probe_not_configured',
          },
        ],
      };
    }
    if (request['command'] == 'GetStatus' && connection == 'connected') {
      ++connectedStatusReads;
      if (connectedStatusReads > 1 &&
          (scenario.startsWith('changed-exit') ||
              scenario.startsWith('pending-exit'))) {
        observedExit = '9.9.9.9';
      }
    }
    if (request['command'] == 'GetStatus' &&
        scenario == 'network-change-tun' &&
        ++statusReads == 2) {
      networkChanged = true;
      connection = 'paused';
    }
    if (request['command'] == 'GetStatus' &&
        scenario == 'paused-system-proxy' &&
        ++statusReads >= 2) {
      connection = 'paused';
    }
    if (request['command'] == 'Connect' ||
        request['command'] == 'SwitchResource') {
      lastProfile = request['profile'] as Map<String, dynamic>;
      ++connects;
      networkChanged = false;
      // Observation is independent from API metadata/request contents.
      observedExit =
          scenario.startsWith('unknown-exit') ||
              scenario.startsWith('pending-exit') ||
              scenario.startsWith('probe-failure')
          ? null
          : scenario.startsWith('empty-exit')
          ? ''
          : scenario.startsWith('invalid-exit')
          ? '10.0.0.1'
          : scenario.startsWith('changed-exit') ||
                scenario.startsWith('diagnostic-change')
          ? '8.8.8.8'
          : '1.1.1.1';
      connectedStatusReads = 0;
      previousContext = context;
      context = {
        'schemaVersion': 1,
        'runtimeEpoch': 'fixture-runtime',
        'clientEpoch': 'fixture-client',
        'logicalSessionId': 'session-$connects',
        'attemptId': 'attempt-$connects',
        'resourceKey': request['profile']['key'],
      };
      connection = 'connected';
      if (scenario == 'failed-prepare-tun') connection = 'failed';
      if (scenario == 'switch-prepare-retry-tun' && connects == 2 ||
          scenario == 'first-prepare-retry-tun' && connects == 1) {
        connection = 'failed';
      }
    } else if (request['command'] == 'Disconnect') {
      connection = 'disconnected'; // Real Go host retains context here.
    } else if (request['command'] == 'Pause') {
      connection = 'paused';
    }
    final value = snapshot(request);
    if (scenario.startsWith('stale-observation') && connectedStatusReads > 1) {
      poison(value, 'attempt');
    }
    return {
      'id': request['id'],
      'ok': true,
      if (request['command'] == 'Hello' || request['command'] == 'GetStatus')
        'runtimeEpoch': 'fixture-runtime',
      if (request['command'] == 'Hello')
        'capabilities': [
          'local_http_proxy',
          'socks5',
          'exit_ipv4_verification',
        ],
      'snapshot': value,
    };
  }

  // Keep stderr larger than a pipe buffer to prove it is drained, never logged.
  if (scenario == 'stderr') {
    stderr.write(List.filled(10000, 'fixture-sensitive-stderr').join());
    await stderr.flush();
  }

  stdin.transform(utf8.decoder).transform(const LineSplitter()).listen((line) {
    final request = jsonDecode(line) as Map<String, dynamic>;
    final command = request['command'] as String;
    commands.add(command);
    ++requests;
    if (command == 'Hello') {
      firstId = request['id'] as String;
      if (scenario == 'hello-rejected') {
        send({'id': request['id'], 'ok': false, 'error': 'OWNER_MISMATCH'});
      } else if (scenario == 'hello-timeout') {
        // Wait until parent closes stdin.
      } else {
        final response = reply(request);
        if (scenario == 'hello:missing-top-runtime') {
          response.remove('runtimeEpoch');
        } else if (scenario.startsWith('hello:')) {
          poison(response['snapshot'], scenario.substring(6));
        }
        send(response);
      }
      return;
    }
    if (command == 'Disconnect') {
      if (scenario == 'slow-system-proxy') {
        Timer(const Duration(milliseconds: 300), () => send(reply(request)));
        return;
      }
      if (scenario == 'disconnect-rejected') {
        send({'id': request['id'], 'ok': false, 'error': 'OWNERSHIP_LOST'});
      } else if (scenario == 'idle-host') {
        send(reply(request));
      } else if (scenario == 'unknown-gate') {
        final response = reply(request);
        (response['snapshot'] as Map)['gateKnown'] = false;
        send(response);
      } else if (scenario == 'disconnect-open-gate') {
        final response = reply(request);
        (response['snapshot'] as Map)['businessGateOpen'] = true;
        send(response);
      } else if (scenario == 'disconnect-applied-proxy' ||
          scenario == 'disconnect-pending-recovery') {
        final response = reply(request);
        response['snapshot'][scenario == 'disconnect-applied-proxy'
                ? 'systemProxyApplied'
                : 'proxyRecoveryRequired'] =
            true;
        send(response);
      } else if (scenario != 'disconnect-timeout') {
        final response = reply(request);
        if (scenario.startsWith('stop:')) {
          poison(response['snapshot'], scenario.substring(5));
        }
        send(response);
      }
      return;
    }
    if (command == 'Connect' && scenario == 'reject-second' && connects > 0) {
      send({
        'id': request['id'],
        'ok': false,
        'error': 'INVALID_CONFIGURATION',
        'snapshot': snapshot(request),
      });
      return;
    }
    if (const {
          'switch-prepare-retry-tun',
          'first-prepare-retry-tun',
        }.contains(scenario) &&
        context != null &&
        const {'Connect', 'SwitchResource'}.contains(command)) {
      final same =
          jsonEncode(context!['resourceKey']) ==
          jsonEncode(request['profile']['key']);
      if (command == 'Connect' && !same ||
          command == 'SwitchResource' && same) {
        send({
          'id': request['id'],
          'ok': false,
          'error': 'SESSION_RESOURCE_MISMATCH',
          'snapshot': snapshot(request),
        });
        return;
      }
    }
    final shouldPoison =
        (command == 'Connect' && scenario.startsWith('connect:')) ||
        (command == 'GetStatus' && scenario.startsWith('status:')) ||
        (command == 'GetStatus' && scenario.startsWith('event:'));
    if (shouldPoison) {
      final response = reply(request);
      final defect = scenario.substring(scenario.indexOf(':') + 1);
      if (defect == 'top-runtime') {
        response['runtimeEpoch'] = 'wrong-runtime';
      } else {
        poison(response['snapshot'], defect);
      }
      if (scenario.startsWith('event:')) {
        send({'event': 'status', 'snapshot': response['snapshot']});
        send({...response, 'snapshot': snapshot(request)});
      } else {
        send(response);
      }
      return;
    }
    switch (scenario) {
      case 'crash':
        exit(17);
      case 'timeout':
        return;
      case 'oversized':
        stdout.add(
          List.filled(65537, 120),
        ); // No newline: parser must bound it.
        return;
      case 'malformed':
        stdout.writeln('{secret-not-valid-json');
        return;
      case 'invalid-utf8':
        stdout.add([0xff, 10]);
        return;
      case 'wrong-id':
        send({'id': 'not-the-request', 'ok': true, 'snapshot': {}});
        return;
      case 'stale-id':
        send({'id': firstId, 'ok': true, 'snapshot': {}});
        return;
      case 'duplicate-reply':
        final line = jsonEncode(reply(request));
        stdout.write('$line\n$line\n');
        return;
      case 'truncated':
        stdout.write('{"id":');
        unawaited(stdout.flush().then((_) => exit(0)));
        return;
      case 'boundary-size':
        final response = reply(request);
        (response['snapshot'] as Map)['padding'] = '';
        final padding = 65536 - utf8.encode(jsonEncode(response)).length;
        (response['snapshot'] as Map)['padding'] = 'x' * padding;
        send(response);
        return;
      case 'error-secret':
        send({'id': request['id'], 'ok': false, 'error': 'password=private'});
        return;
      case 'missing-snapshot':
        send({'id': request['id'], 'ok': true});
        return;
      case 'rejected':
        send({'id': request['id'], 'ok': false, 'error': 'INVALID_MODE'});
        return;
      case 'fragmented':
        final encoded = utf8.encode(jsonEncode(reply(request)));
        for (var index = 0; index < encoded.length; index += 3) {
          stdout.add(
            encoded.sublist(index, (index + 3).clamp(0, encoded.length)),
          );
        }
        stdout.add([13, 10]);
        return;
      case 'serial':
        if (busy) exit(19);
        busy = true;
        Timer(const Duration(milliseconds: 40), () {
          busy = false;
          send({'event': 'status', 'snapshot': snapshot(request)});
          send(reply(request));
        });
        return;
      default:
        send(reply(request));
    }
  }, onDone: () => waiting.complete());
  await waiting.future;
  if (scenario == 'detach-tun' &&
      (commands.contains('Disconnect') || !commands.contains('Pause'))) {
    exit(51);
  }
  if (scenario == 'hang-exit') {
    Timer.periodic(const Duration(seconds: 1), (_) {});
    await Completer<void>().future;
  }
  await stdout.flush();
  exit(scenario == 'bad-exit' ? 23 : 0);
}

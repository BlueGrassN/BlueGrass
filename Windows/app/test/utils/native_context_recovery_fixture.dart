// Isolated pipe peer for NativeClient recovery tests. No network or OS changes.
import 'dart:convert';
import 'dart:io';

dynamic copy(dynamic value) => jsonDecode(jsonEncode(value));

Future<void> main(List<String> args) async {
  final scenario = args.single;
  const runtime = 'fixture-runtime';
  Map<String, dynamic>? context, previous;
  var state = 'disconnected', connects = 0, switches = 0;
  final commands = <String>[];

  Map<String, dynamic> snapshot() => {
    'runtimeEpoch': runtime,
    if (context != null) 'context': copy(context),
    'connection': state,
    'actualMode': state == 'disconnected' ? 'none' : 'tun',
    'gateKnown': true,
    'businessGateOpen': state == 'connected',
    'protectionIntent': state != 'disconnected',
    'protection': state == 'connected' ? 'wfp_forwarding' : 'wfp_blocking',
    'systemProxyApplied': false,
    'proxyRecoveryRequired': false,
    'commands': List.of(commands),
  };

  Map<String, dynamic> binding(Map<String, dynamic> request) => {
    'schemaVersion': 1,
    'runtimeEpoch': runtime,
    'clientEpoch': 'fixture-client',
    'logicalSessionId': connects > 1 ? 'committed-session' : 'session-A',
    'attemptId': 'attempt-$connects-$switches',
    'resourceKey': copy(request['profile']['key']),
  };

  void send(Map<String, dynamic> reply) => stdout.writeln(jsonEncode(reply));

  await for (final line
      in stdin.transform(utf8.decoder).transform(const LineSplitter())) {
    final request = jsonDecode(line) as Map<String, dynamic>;
    final command = request['command'] as String;
    commands.add(command);
    if (command == 'Hello') {
      final value = snapshot();
      if (scenario == 'on-hello') value['contextRecovery'] = {'version': 1};
      send({
        'id': request['id'],
        'ok': true,
        'runtimeEpoch': runtime,
        'snapshot': value,
      });
      continue;
    }
    if (command == 'Connect') {
      connects++;
      context = binding(request);
      state = 'connected';
    } else if (command == 'SwitchResource' ||
        command == 'GetStatus' && scenario == 'on-status') {
      switches++;
      if (scenario == 'before-commit') {
        state = 'paused';
        send({
          'id': request['id'],
          'ok': false,
          'error': 'ENGINE_FAILURE',
          'snapshot': snapshot(),
        });
        continue;
      }
      if (scenario == 'queued-stale' && switches == 1) {
        context = binding(request);
        state = 'connected';
        send({'id': request['id'], 'ok': true, 'snapshot': snapshot()});
        continue;
      }
      previous = copy(context) as Map<String, dynamic>;
      context = null;
      state = 'paused';
      final marker = <String, dynamic>{
        'version': 1,
        'reason': 'resource_switch_committed',
        'requestId': request['id'],
        'previousContext': copy(request['context'] ?? previous),
        'resourceKey': copy(
          request['profile']?['key'] ?? previous['resourceKey'],
        ),
      };
      final value = snapshot()..['contextRecovery'] = marker;
      final reply = <String, dynamic>{
        'id': request['id'],
        'ok': false,
        'error': 'ELIG-003',
        'snapshot': value,
      };
      switch (scenario) {
        case 'missing-marker':
          value.remove('contextRecovery');
        case 'null-marker':
          value['contextRecovery'] = null;
        case 'extra-marker-field':
          marker['extra'] = true;
        case 'missing-marker-field':
          marker.remove('requestId');
        case 'wrong-version':
          marker['version'] = 2;
        case 'float-version':
          marker['version'] = 1.0;
        case 'wrong-reason':
          marker['reason'] = 'other';
        case 'wrong-request-id':
          marker['requestId'] = 'other-request';
        case 'wrong-runtime':
          value['runtimeEpoch'] = 'other-runtime';
        case 'wrong-envelope-runtime':
          reply['runtimeEpoch'] = 'other-runtime';
        case 'wrong-client':
          marker['previousContext']['clientEpoch'] = 'other-client';
        case 'wrong-session':
          marker['previousContext']['logicalSessionId'] = 'other-session';
        case 'wrong-attempt':
          marker['previousContext']['attemptId'] = 'other-attempt';
        case 'wrong-old-owner':
          marker['previousContext']['resourceKey']['accountKey'] =
              'other-account';
        case 'wrong-old-resource':
          marker['previousContext']['resourceKey']['sourceId'] =
              'other-resource';
        case 'wrong-target':
          marker['resourceKey']['sourceId'] = 'other-resource';
        case 'context-present':
          value['context'] = copy(previous);
        case 'context-null':
          value['context'] = null;
        case 'context-malformed':
          value['context'] = <String, dynamic>{};
        case 'gate-open':
          value['businessGateOpen'] = true;
        case 'gate-unknown':
          value['gateKnown'] = false;
        case 'not-paused':
          value['connection'] = 'failed';
        case 'not-tun':
          value['actualMode'] = 'system_proxy';
        case 'no-protection':
          value['protectionIntent'] = false;
        case 'wrong-protection':
          value['protection'] = 'wfp_forwarding';
        case 'proxy-applied':
          value['systemProxyApplied'] = true;
        case 'proxy-recovery':
          value['proxyRecoveryRequired'] = true;
        case 'missing-proxy-recovery':
          value.remove('proxyRecoveryRequired');
        case 'success':
          reply['ok'] = true;
        case 'event':
          send({'event': 'status', 'snapshot': value});
      }
      send(reply);
      continue;
    } else if (command == 'GetStatus' &&
        scenario == 'old-event-after-recovery' &&
        context == null) {
      send({'event': 'status', 'snapshot': snapshot()..['context'] = previous});
    } else if (command == 'Disconnect' || command == 'ReleaseProtection') {
      state = 'disconnected';
    } else if (command == 'Pause') {
      state = 'paused';
    }
    final value = snapshot();
    if (command == 'Diagnose') value['receivedContext'] = request['context'];
    send({'id': request['id'], 'ok': true, 'snapshot': value});
  }
}

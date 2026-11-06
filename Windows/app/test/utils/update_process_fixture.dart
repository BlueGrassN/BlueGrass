import 'dart:async';
import 'dart:convert';
import 'dart:io';

Future<void> main(List<String> arguments) async {
  final scenario = arguments.single;
  final req = jsonDecode(
    await stdin.transform(utf8.decoder).join(),
  ) as Map<String, dynamic>;
  if (req.keys.any(
    (k) => !{
      'v',
      'id',
      'op',
      'candidateId',
      'allowMetered',
      'installConsent',
    }.contains(k),
  )) {
    exit(2);
  }
  if (scenario == 'hang') {
    await Future<void>.delayed(const Duration(seconds: 30));
    return;
  }
  if (scenario == 'oversized') {
    stdout.writeln('x' * (64 * 1024 + 1));
    return;
  }
  const candidate =
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
  final snapshot = <String, dynamic>{
    'state': req['op'] == 'launch'
        ? 'launch_requested'
        : req['op'] == 'download'
        ? 'ready'
        : 'available',
    'candidateId': scenario == 'wrong-candidate' ? 'b' * 64 : candidate,
    'version': '1.1.0',
    'build': 11,
    'size': 7,
    'notes': 'Fixture',
    'minimumVersion': '1.0.0',
    'unsupported': false,
    'downloaded': 7,
    'remaining': 0,
    'fromCache': false,
  };
  final reply = <String, dynamic>{
    'v': 1,
    'id': scenario == 'wrong-id' ? 'wrong' : req['id'],
    'ok': true,
    'snapshot': snapshot,
  };
  if (scenario == 'unconfigured' ||
      req['op'] == 'download' && req['allowMetered'] != true) {
    stdout.writeln(
      jsonEncode({
        'v': 1,
        'id': req['id'],
        'ok': false,
        'error': scenario == 'unconfigured'
            ? 'UPDATE_UNCONFIGURED'
            : 'UPDATE_WAITING_NETWORK',
      }),
    );
    return;
  }
  if (req['op'] == 'startup_policy' &&
      (req.length != 3 ||
          req.keys.any((k) => !{'v', 'id', 'op'}.contains(k)))) {
    exit(2);
  }
  if (scenario.startsWith('startup-')) {
    reply.remove('snapshot');
    final policy = <String, dynamic>{
      'state': 'verified',
      'minimumVersion': '1.1.0',
      'unsupported': true,
      'manifestExpired': true,
    };
    reply['startupPolicy'] = policy;
    switch (scenario) {
      case 'startup-absent':
        policy.remove('minimumVersion');
        policy.addAll({
          'state': 'no_history',
          'unsupported': false,
          'manifestExpired': false,
        });
      case 'startup-corrupt':
        reply.remove('startupPolicy');
        reply.addAll({'ok': false, 'error': 'UPDATE_STATE_INVALID'});
      case 'startup-error-policy':
        reply.addAll({'ok': false, 'error': 'UPDATE_STATE_INVALID'});
      case 'startup-mixed':
        reply['snapshot'] = snapshot;
      case 'startup-candidate':
        policy['candidateId'] = candidate;
      case 'startup-bad-absent':
        policy['state'] = 'no_history';
      case 'startup-progress':
        reply['event'] = 'progress';
      case 'startup-no-expiry':
        policy.remove('manifestExpired');
      case 'startup-missing-minimum':
        policy.remove('minimumVersion');
      case 'startup-null-minimum':
        policy.addAll({
          'state': 'no_history',
          'minimumVersion': null,
          'unsupported': false,
          'manifestExpired': false,
        });
    }
  }
  if (req['op'] == 'download' && scenario == 'normal') {
    stdout.writeln(
      jsonEncode({
        ...reply,
        'event': 'progress',
        'snapshot': {
          ...snapshot,
          'state': 'downloading',
          'downloaded': 3,
          'remaining': 4,
        },
      }),
    );
  }
  stdout.writeln(jsonEncode(reply));
  if (scenario == 'duplicate-final') stdout.writeln(jsonEncode(reply));
}

import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  final f = fixture('connection/generations.json');
  final now = DateTime.parse(f['nowUtc'] as String);
  ControlContext ctx(String id) =>
      ControlContext.fromJson(f['context$id'] as Map<String, dynamic>);
  ConnectionProfile profile(String id,
          {DateTime? expiry, String? expected = '8.8.8.8'}) =>
      (prepareProfile(
              node(
                  resourceKey: ctx(id).resourceKey,
                  expiry: expiry,
                  expected: expected),
              now) as Ready)
          .profile;
  NativeConfirmed confirm(String id, int sequence,
          {ConnectionPhase phase = ConnectionPhase.connected,
          DateTime? time,
          String? ip = '8.8.8.8',
          bool snapshot = false}) =>
      NativeConfirmed(ctx(id),
          eventSequence: sequence,
          phase: phase,
          protection: ProtectionState.forwarding,
          effectiveNowUtc: time ?? now,
          verifiedExitIpv4: ip,
          isSnapshot: snapshot);
  for (final c in f['cases'] as List) {
    test(c['caseId'] as String, () {
      var state = ConnectionState();
      for (final e in c['events'] as List) {
        final event = switch (e['type']) {
          'connect' => ConnectRequested(
              ctx(e['context'] as String), profile(e['context'] as String)),
          'disconnect' => const DisconnectRequested(),
          'confirmed' => confirm(e['context'] as String, e['sequence'] as int,
              phase: ConnectionPhase.values.byName(e['phase'] as String),
              ip: e['verifiedExitIpv4'] as String?),
          _ => throw StateError('Unknown fixture event'),
        };
        state = reduceConnection(state, event);
      }
      expect(state.phase.name, c['expectedPhase']);
      expect(state.context?.attemptId, c['expectedAttempt']);
      if (c.containsKey('expectedObservedIpv4')) {
        expect(state.verifiedExitIpv4, c['expectedObservedIpv4']);
      }
    });
  }
  test(
      'connected observation is optional and never defaults to expected metadata',
      () {
    for (final expected in [null, '8.8.8.8', 'not-an-ip']) {
      for (final observed in [null, '1.1.1.1']) {
        var s = reduceConnection(ConnectionState(),
            ConnectRequested(ctx('A'), profile('A', expected: expected)));
        s = reduceConnection(s, confirm('A', 1, ip: observed));
        expect(s.phase, ConnectionPhase.connected);
        expect(s.verifiedExitIpv4, observed);
        expect(s.effects, [ConnectionEffect.recordRecentSuccess]);
      }
    }
  });
  test(
      'authoritative connected snapshot with unknown IP clears old observation',
      () {
    var s = reduceConnection(
        ConnectionState(), ConnectRequested(ctx('A'), profile('A')));
    s = reduceConnection(s, confirm('A', 1));
    expect(s.verifiedExitIpv4, '8.8.8.8');
    s = reduceConnection(s, confirm('A', 4, ip: null, snapshot: true));
    expect(s.phase, ConnectionPhase.connected);
    expect(s.verifiedExitIpv4, isNull);
    expect(s.effects, isEmpty);
    s = reduceConnection(s, confirm('A', 5, ip: '1.1.1.1'));
    expect(s.phase, ConnectionPhase.connected);
    expect(s.verifiedExitIpv4, '1.1.1.1');
    expect(s.effects, isEmpty);
  });
  test('metadata-only revision preserves connected phase and observed IP', () {
    var s = reduceConnection(
        ConnectionState(), ConnectRequested(ctx('A'), profile('A')));
    s = reduceConnection(s, confirm('A', 1));
    var revision = 1;
    for (final expected in ['1.1.1.1', null, 'not-an-ip']) {
      s = reduceConnection(
          s,
          ResourceUpdateAccepted(ctx('A'),
              revision: revision++, profile: profile('A', expected: expected)));
      expect(s.phase, ConnectionPhase.connected);
      expect(s.profile?.expectedExitIpv4, expected);
      expect(s.verifiedExitIpv4, '8.8.8.8');
      expect(s.effects, [ConnectionEffect.applyResourceUpdate]);
    }
  });
  for (final ip
      in fixture('eligibility/cases.json')['invalidObservedIps'] as List) {
    test(
        'malformed observed IP rejected even when expected metadata agrees: $ip',
        () {
      var s = reduceConnection(ConnectionState(),
          ConnectRequested(ctx('A'), profile('A', expected: ip as String)));
      s = reduceConnection(s, confirm('A', 1, ip: ip));
      expect(s.phase, ConnectionPhase.pausing);
      expect(s.verifiedExitIpv4, isNull);
      expect(s.effects, [ConnectionEffect.pause]);
    });
  }
  test('unknown observation retains deadline and forwarding protection checks',
      () {
    final expiry = now.add(const Duration(seconds: 1));
    final initial = reduceConnection(
        ConnectionState(),
        ConnectRequested(
            ctx('A'), profile('A', expiry: expiry, expected: null)));
    for (final protection in ProtectionState.values) {
      final s = reduceConnection(
          initial,
          NativeConfirmed(ctx('A'),
              eventSequence: 1,
              phase: ConnectionPhase.connected,
              protection: protection,
              effectiveNowUtc: now));
      expect(
          s.phase,
          protection == ProtectionState.forwarding
              ? ConnectionPhase.connected
              : ConnectionPhase.pausing);
    }
    expect(
        reduceConnection(initial, confirm('A', 1, ip: null, time: expiry))
            .phase,
        ConnectionPhase.pausing);
  });
  test('exact deadline pauses without claiming protection released', () {
    final expiry = now.add(const Duration(seconds: 1));
    var s = reduceConnection(ConnectionState(),
        ConnectRequested(ctx('A'), profile('A', expiry: expiry)));
    s = reduceConnection(s, confirm('A', 1));
    s = reduceConnection(s, ClockAdvanced(expiry));
    expect(s.phase, ConnectionPhase.pausing);
    expect(s.protection,
        ProtectionState.forwarding); // last actual state, pending ack
    expect(s.effects, [ConnectionEffect.pause]);
    s = reduceConnection(
        s,
        NativeConfirmed(ctx('A'),
            eventSequence: 2,
            phase: ConnectionPhase.paused,
            protection: ProtectionState.blocking,
            effectiveNowUtc: expiry));
    expect(s.phase, ConnectionPhase.paused);
    expect(s.protection, ProtectionState.blocking);
  });
  test(
      'expired native success and duplicate/gapped events cannot claim connected',
      () {
    final expiry = now.add(const Duration(seconds: 1));
    var s = reduceConnection(ConnectionState(),
        ConnectRequested(ctx('A'), profile('A', expiry: expiry)));
    s = reduceConnection(s, confirm('A', 1, time: expiry));
    expect(s.phase, ConnectionPhase.pausing);
    s = reduceConnection(
        ConnectionState(), ConnectRequested(ctx('A'), profile('A')));
    s = reduceConnection(s, confirm('A', 1));
    final duplicate =
        reduceConnection(s, confirm('A', 1, phase: ConnectionPhase.failed));
    expect(duplicate.phase, ConnectionPhase.connected);
    expect(duplicate.effects, isEmpty);
    final gap =
        reduceConnection(s, confirm('A', 4, phase: ConnectionPhase.failed));
    expect(gap.effects, [ConnectionEffect.getStatus]);
    expect(gap.phase, ConnectionPhase.connected);
    expect(
        reduceConnection(gap,
                confirm('A', 4, phase: ConnectionPhase.failed, snapshot: true))
            .phase,
        ConnectionPhase.failed);
  });
  test('old resource update is ignored and expiry-only change needs native ack',
      () {
    var s = reduceConnection(
        ConnectionState(), ConnectRequested(ctx('A'), profile('A')));
    s = reduceConnection(s, confirm('A', 1));
    final future = profile('A', expiry: now.add(const Duration(days: 3)));
    s = reduceConnection(
        s, ResourceUpdateAccepted(ctx('A'), revision: 2, profile: future));
    expect(s.effects, [ConnectionEffect.applyResourceUpdate]);
    expect(s.profile?.validUntilUtc, future.validUntilUtc);
    s = reduceConnection(
        s, ResourceUpdateAccepted(ctx('A'), revision: 1, unavailable: true));
    expect(s.phase, ConnectionPhase.connected);
    expect(s.resourceRevision, 2);
    s = reduceConnection(s, const DisconnectRequested());
    s = reduceConnection(
        s, ResourceUpdateAccepted(ctx('A'), revision: 3, profile: future));
    expect(s.phase, ConnectionPhase.stopping);
    expect(s.effects, isEmpty);
  });
  test('entry or credential changes still pause without any expected metadata',
      () {
    var s = reduceConnection(ConnectionState(),
        ConnectRequested(ctx('A'), profile('A', expected: null)));
    s = reduceConnection(s, confirm('A', 1, ip: null));
    for (final replacement in [
      NodeRecord(
          key: ctx('A').resourceKey,
          name: 'fixture',
          protocol: 'socks5',
          host: 'new.example.invalid',
          port: 1080),
      NodeRecord(
          key: ctx('A').resourceKey,
          name: 'fixture',
          protocol: 'socks5',
          host: 'entry.example.invalid',
          port: 1081),
      NodeRecord(
          key: ctx('A').resourceKey,
          name: 'fixture',
          protocol: 'socks5',
          host: 'entry.example.invalid',
          port: 1080,
          username: 'fixture',
          password: 'fixture'),
    ]) {
      final updated = reduceConnection(
          s,
          ResourceUpdateAccepted(ctx('A'),
              revision: 1,
              profile: (prepareProfile(replacement, now) as Ready).profile));
      expect(updated.phase, ConnectionPhase.pausing);
      expect(updated.effects, [ConnectionEffect.applyResourceUpdate]);
    }
  });
  test('namespace/client/runtime/attempt must all match native confirmation',
      () {
    final s = reduceConnection(
        ConnectionState(), ConnectRequested(ctx('A'), profile('A')));
    for (final field in [
      'clientEpoch',
      'runtimeEpoch',
      'logicalSessionId',
      'attemptId'
    ]) {
      final alien =
          ControlContext.fromJson({...ctx('A').toJson(), field: 'other'});
      expect(
          reduceConnection(
                  s,
                  NativeConfirmed(alien,
                      eventSequence: 1,
                      phase: ConnectionPhase.connected,
                      protection: ProtectionState.forwarding,
                      effectiveNowUtc: now,
                      verifiedExitIpv4: '8.8.8.8'))
              .phase,
          ConnectionPhase.connecting);
    }
  });
  test('invalid IPC context is rejected before reduction', () {
    for (final json in [
      {...ctx('A').toJson(), 'schemaVersion': 2},
      {...ctx('A').toJson(), 'attemptId': ''},
      {...ctx('A').toJson(), 'extra': true},
    ]) {
      expect(() => ControlContext.fromJson(json), throwsFormatException);
    }
  });
  test('terminal failure cannot revive via late intermediate event', () {
    var s = reduceConnection(
        ConnectionState(), ConnectRequested(ctx('A'), profile('A')));
    s = reduceConnection(s, confirm('A', 1, phase: ConnectionPhase.failed));
    s = reduceConnection(s, confirm('A', 2, phase: ConnectionPhase.connecting));
    s = reduceConnection(s, confirm('A', 3));
    expect(s.phase, ConnectionPhase.failed);
    expect(s.effects, isNot(contains(ConnectionEffect.recordRecentSuccess)));
  });
  test('typed invalid context cannot emit a Connect effect', () {
    final invalid = ControlContext(
        clientEpoch: '',
        runtimeEpoch: 'runtime-1',
        logicalSessionId: 'session',
        attemptId: 'attempt',
        resourceKey: ctx('A').resourceKey);
    expect(
        reduceConnection(
                ConnectionState(), ConnectRequested(invalid, profile('A')))
            .effects,
        isEmpty);
  });
}

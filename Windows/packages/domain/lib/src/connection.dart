import 'profile.dart';
import 'resources.dart';

final class ControlContext {
  const ControlContext(
      {this.schemaVersion = 1,
      required this.clientEpoch,
      required this.runtimeEpoch,
      required this.logicalSessionId,
      required this.attemptId,
      required this.resourceKey});
  final int schemaVersion;
  final String clientEpoch, runtimeEpoch, logicalSessionId, attemptId;
  final ResourceKey resourceKey;
  factory ControlContext.fromJson(Map<String, dynamic> j) {
    const strings = {
      'clientEpoch',
      'runtimeEpoch',
      'logicalSessionId',
      'attemptId'
    };
    const fields = {...strings, 'schemaVersion', 'resourceKey'};
    if (j.length != fields.length ||
        !j.keys.every(fields.contains) ||
        j['schemaVersion'] != 1 ||
        strings
            .any((f) => j[f] is! String || (j[f] as String).trim().isEmpty) ||
        j['resourceKey'] is! Map<String, dynamic>) {
      throw const FormatException('Invalid control context');
    }
    return ControlContext(
        schemaVersion: j['schemaVersion'] as int,
        clientEpoch: j['clientEpoch'] as String,
        runtimeEpoch: j['runtimeEpoch'] as String,
        logicalSessionId: j['logicalSessionId'] as String,
        attemptId: j['attemptId'] as String,
        resourceKey:
            ResourceKey.fromJson(j['resourceKey'] as Map<String, dynamic>));
  }
  Map<String, dynamic> toJson() => {
        'schemaVersion': schemaVersion,
        'clientEpoch': clientEpoch,
        'runtimeEpoch': runtimeEpoch,
        'logicalSessionId': logicalSessionId,
        'attemptId': attemptId,
        'resourceKey': resourceKey.toJson()
      };
  @override
  bool operator ==(Object other) =>
      other is ControlContext &&
      schemaVersion == other.schemaVersion &&
      clientEpoch == other.clientEpoch &&
      runtimeEpoch == other.runtimeEpoch &&
      logicalSessionId == other.logicalSessionId &&
      attemptId == other.attemptId &&
      resourceKey == other.resourceKey;
  @override
  int get hashCode => Object.hash(schemaVersion, clientEpoch, runtimeEpoch,
      logicalSessionId, attemptId, resourceKey);
}

enum ConnectionPhase {
  idle,
  connecting,
  connected,
  pausing,
  paused,
  stopping,
  failed
}

enum ProtectionState { unknown, inactive, blocking, forwarding }

enum ConnectionEffect {
  connect,
  pause,
  disconnect,
  getStatus,
  applyResourceUpdate,
  recordRecentSuccess
}

final class ConnectionState {
  ConnectionState(
      {this.phase = ConnectionPhase.idle,
      this.protection = ProtectionState.unknown,
      this.context,
      this.profile,
      this.lastEventSequence = 0,
      this.resourceRevision = 0,
      this.verifiedExitIpv4,
      List<ConnectionEffect> effects = const []})
      : effects = List.unmodifiable(effects);
  final ConnectionPhase phase;
  final ProtectionState protection;
  final ControlContext? context;
  final ConnectionProfile? profile;
  final int lastEventSequence, resourceRevision;
  final String? verifiedExitIpv4;
  final List<ConnectionEffect> effects;
}

sealed class ConnectionEvent {
  const ConnectionEvent();
}

final class ConnectRequested extends ConnectionEvent {
  const ConnectRequested(this.context, this.profile);
  final ControlContext context;
  final ConnectionProfile profile;
}

final class DisconnectRequested extends ConnectionEvent {
  const DisconnectRequested();
}

final class PauseRequested extends ConnectionEvent {
  const PauseRequested();
}

final class ClockAdvanced extends ConnectionEvent {
  const ClockAdvanced(this.effectiveNowUtc);
  final DateTime effectiveNowUtc;
}

final class ResourceUpdateAccepted extends ConnectionEvent {
  const ResourceUpdateAccepted(this.context,
      {required this.revision, this.profile, this.unavailable = false});
  final ControlContext context;
  final int revision;
  final ConnectionProfile? profile;
  final bool unavailable;
}

final class NativeConfirmed extends ConnectionEvent {
  const NativeConfirmed(this.context,
      {required this.eventSequence,
      required this.phase,
      required this.protection,
      required this.effectiveNowUtc,
      this.verifiedExitIpv4,
      this.isSnapshot = false});
  final ControlContext context;
  final int eventSequence;
  final ConnectionPhase phase;
  final ProtectionState protection;
  final DateTime effectiveNowUtc;
  final String? verifiedExitIpv4;
  final bool isSnapshot;
}

ConnectionState reduceConnection(ConnectionState state, ConnectionEvent event) {
  ConnectionState next(
          {ConnectionPhase? phase,
          ProtectionState? protection,
          int? sequence,
          int? revision,
          ConnectionProfile? profile,
          String? verifiedIp,
          bool clearVerification = false,
          List<ConnectionEffect> effects = const []}) =>
      ConnectionState(
          phase: phase ?? state.phase,
          protection: protection ?? state.protection,
          context: state.context,
          profile: profile ?? state.profile,
          lastEventSequence: sequence ?? state.lastEventSequence,
          resourceRevision: revision ?? state.resourceRevision,
          verifiedExitIpv4:
              clearVerification ? null : verifiedIp ?? state.verifiedExitIpv4,
          effects: effects);
  final terminating = {
    ConnectionPhase.stopping,
    ConnectionPhase.pausing,
    ConnectionPhase.paused,
    ConnectionPhase.idle
  }.contains(state.phase);
  switch (event) {
    case ConnectRequested():
      try {
        ControlContext.fromJson(event.context.toJson());
      } on FormatException {
        return next();
      }
      if (event.context.schemaVersion != 1 ||
          event.context.resourceKey != event.profile.key ||
          event.context == state.context) return next();
      return ConnectionState(
          phase: ConnectionPhase.connecting,
          protection: state.protection,
          context: event.context,
          profile: event.profile,
          effects: [ConnectionEffect.connect]);
    case DisconnectRequested():
      if (state.context == null ||
          state.phase == ConnectionPhase.stopping ||
          state.phase == ConnectionPhase.idle) return next();
      return next(
          phase: ConnectionPhase.stopping,
          clearVerification: true,
          effects: [ConnectionEffect.disconnect]);
    case PauseRequested():
      if (state.context == null || terminating) return next();
      return next(
          phase: ConnectionPhase.pausing,
          clearVerification: true,
          effects: [ConnectionEffect.pause]);
    case ClockAdvanced():
      final deadline = state.profile?.validUntilUtc;
      if (deadline == null ||
          event.effectiveNowUtc.isBefore(deadline) ||
          terminating) return next();
      return next(
          phase: ConnectionPhase.pausing,
          clearVerification: true,
          effects: [ConnectionEffect.pause]);
    case ResourceUpdateAccepted():
      if (event.context != state.context ||
          event.revision <= state.resourceRevision ||
          terminating) return next();
      if (event.unavailable)
        return next(
            phase: ConnectionPhase.pausing,
            revision: event.revision,
            clearVerification: true,
            effects: [ConnectionEffect.pause]);
      final profile = event.profile;
      if (profile == null || profile.key != state.context!.resourceKey)
        return next();
      final old = state.profile;
      final changed = old == null ||
          profile.protocol != old.protocol ||
          profile.host != old.host ||
          profile.port != old.port ||
          profile.username != old.username ||
          profile.password != old.password ||
          profile.protocolConfig != old.protocolConfig;
      return next(
          profile: profile,
          revision: event.revision,
          phase: changed ? ConnectionPhase.pausing : state.phase,
          clearVerification: changed,
          effects: [ConnectionEffect.applyResourceUpdate]);
    case NativeConfirmed():
      if (event.context != state.context ||
          event.eventSequence <= state.lastEventSequence) return next();
      if (state.phase == ConnectionPhase.failed &&
          {ConnectionPhase.connecting, ConnectionPhase.connected}
              .contains(event.phase)) return next();
      if (!event.isSnapshot &&
          event.eventSequence != state.lastEventSequence + 1) {
        return next(effects: [ConnectionEffect.getStatus]);
      }
      if (event.phase == ConnectionPhase.connected) {
        if (terminating || state.phase == ConnectionPhase.failed) return next();
        final profile = state.profile;
        if (profile == null ||
            event.verifiedExitIpv4 != null &&
                !isPublicIpv4(event.verifiedExitIpv4) ||
            event.protection != ProtectionState.forwarding ||
            profile.validUntilUtc != null &&
                !event.effectiveNowUtc.isBefore(profile.validUntilUtc!)) {
          return next(
              phase: ConnectionPhase.pausing,
              sequence: event.eventSequence,
              clearVerification: true,
              effects: [ConnectionEffect.pause]);
        }
        return next(
            phase: ConnectionPhase.connected,
            protection: event.protection,
            sequence: event.eventSequence,
            verifiedIp: event.verifiedExitIpv4,
            clearVerification: event.verifiedExitIpv4 == null,
            effects: state.phase == ConnectionPhase.connected
                ? []
                : [ConnectionEffect.recordRecentSuccess]);
      }
      if (state.phase == ConnectionPhase.stopping &&
          event.phase != ConnectionPhase.idle) return next();
      if ({ConnectionPhase.pausing, ConnectionPhase.paused}
              .contains(state.phase) &&
          event.phase == ConnectionPhase.connecting) return next();
      return next(
          phase: event.phase,
          protection: event.protection,
          sequence: event.eventSequence,
          clearVerification: true);
  }
}

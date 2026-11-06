import 'resources.dart';
import 'protocol_config.dart';

final class DomainError {
  const DomainError({required this.code, required this.message});
  final String code, message;
  @override
  String toString() => '$code: $message';
}

sealed class ProfileResult {
  const ProfileResult();
}

final class Ready extends ProfileResult {
  const Ready(this.profile);
  final ConnectionProfile profile;
}

final class Rejected extends ProfileResult {
  const Rejected(this.error);
  final DomainError error;
}

/// Construct through prepareProfile; credential values are never in diagnostics.
final class ConnectionProfile {
  const ConnectionProfile._(this.node);
  final NodeRecord node;
  ResourceKey get key => node.key;
  String get protocol => node.protocol;
  String get host => node.host;
  int get port => node.port;
  String get username => node.username;
  String get password => node.password;
  ProtocolConfig? get protocolConfig => node.protocolConfig;

  /// Optional source metadata; never a connection or observed-IP prerequisite.
  String? get expectedExitIpv4 => node.expectedExitIpv4;
  DateTime? get validUntilUtc => node.expiresAtUtc?.toUtc();
}

ProfileResult prepareProfile(NodeRecord node, DateTime effectiveNowUtc) {
  Rejected reject(String code, String message) =>
      Rejected(DomainError(code: code, message: message));
  try {
    ResourceKey.fromJson(node.key.toJson());
  } on FormatException {
    return reject('SYNC-003', 'Invalid resource identity');
  }
  if (!node.isForwarded)
    return reject('ELIG-001', 'Forwarding is not available');
  if (node.expiresAtUtc != null &&
      !effectiveNowUtc.isBefore(node.expiresAtUtc!)) {
    return reject('ELIG-003', 'Resource has expired');
  }
  if (node.protocol == 'tuic' || node.protocol == 'anytls') {
    return reject('ELIG-008', 'This protocol is not supported by Xray');
  }
  try {
    final explicit = node.protocolConfig;
    if (explicit == null) {
      if (node.protocol != 'socks5') {
        throw const FormatException('INVALID_PROTOCOL_CONFIG');
      }
      ProtocolConfig.legacySocks(
          username: node.username, password: node.password);
    } else if (!explicit.matchesNode(
        protocol: node.protocol,
        username: node.username,
        password: node.password)) {
      throw const FormatException('INVALID_PROTOCOL_CONFIG');
    }
  } on FormatException {
    return reject('ELIG-007', 'Protocol configuration is not supported');
  }
  if (node.host.isEmpty ||
      node.host.trim() != node.host ||
      RegExp(r'[\s/@?#]').hasMatch(node.host) ||
      node.port < 1 ||
      node.port > 65535) {
    return reject('ELIG-005', 'Invalid entry endpoint');
  }
  return Ready(ConnectionProfile._(node));
}

/// Strict dotted-decimal public unicast; excludes private/special-use ranges.
bool isPublicIpv4(String? value) {
  if (value == null ||
      !RegExp(r'^(0|[1-9][0-9]{0,2})(\.(0|[1-9][0-9]{0,2})){3}$')
          .hasMatch(value)) return false;
  final p = value.split('.').map(int.parse).toList();
  if (p.any((n) => n > 255)) return false;
  final a = p[0], b = p[1], c = p[2], d = p[3];
  if (a == 0 || a == 10 || a == 127 || a >= 224) return false;
  if (a == 100 && b >= 64 && b <= 127) return false;
  if (a == 169 && b == 254 || a == 172 && b >= 16 && b <= 31) return false;
  if (a == 192 && (b == 168 || b == 0 && c == 2 || b == 88 && c == 99))
    return false;
  if (a == 192 && b == 0 && c == 0 && d != 9 && d != 10) return false;
  if (a == 198 && (b == 18 || b == 19 || b == 51 && c == 100)) return false;
  if (a == 203 && b == 0 && c == 113) return false;
  return true;
}

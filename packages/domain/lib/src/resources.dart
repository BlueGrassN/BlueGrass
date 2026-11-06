import 'dart:convert';
import 'protocol_config.dart';

/// Presentation only: retain unknown expiry and the first 72 expired hours.
/// This never changes ownership, connection eligibility or API pagination.
bool isResourceVisible(DateTime? expiresAtUtc, DateTime effectiveNowUtc) =>
    expiresAtUtc == null ||
    effectiveNowUtc.toUtc().difference(expiresAtUtc.toUtc()) <=
        const Duration(hours: 72);

/// Identity is scoped to the source and account, never to a display name/IP.
final class ResourceKey {
  const ResourceKey(
      {required this.environment,
      required this.dataSource,
      required this.accountKey,
      this.kind = 'subscription',
      required this.sourceId});
  final String environment, dataSource, accountKey, kind, sourceId;
  Map<String, dynamic> toJson() => {
        'environment': environment,
        'dataSource': dataSource,
        'accountKey': accountKey,
        'kind': kind,
        'sourceId': sourceId
      };
  factory ResourceKey.fromJson(Map<String, dynamic> json) {
    const fields = {
      'environment',
      'dataSource',
      'accountKey',
      'kind',
      'sourceId'
    };
    if (json.length != fields.length ||
        !json.keys.every(fields.contains) ||
        fields.any(
            (f) => json[f] is! String || (json[f] as String).trim().isEmpty) ||
        !{'subscription', 'asset'}.contains(json['kind'])) {
      throw const FormatException('Invalid resource identity');
    }
    return ResourceKey(
        environment: json['environment'] as String,
        dataSource: json['dataSource'] as String,
        accountKey: json['accountKey'] as String,
        kind: json['kind'] as String,
        sourceId: json['sourceId'] as String);
  }
  String get stableId =>
      jsonEncode([environment, dataSource, accountKey, kind, sourceId]);
  @override
  bool operator ==(Object other) =>
      other is ResourceKey &&
      environment == other.environment &&
      dataSource == other.dataSource &&
      accountKey == other.accountKey &&
      kind == other.kind &&
      sourceId == other.sourceId;
  @override
  int get hashCode =>
      Object.hash(environment, dataSource, accountKey, kind, sourceId);
}

final class NodeRecord {
  const NodeRecord(
      {required this.key,
      required this.name,
      this.customerRemark = '',
      required this.protocol,
      required this.host,
      required this.port,
      this.username = '',
      this.password = '',
      this.countryCode = '',
      this.countryName = '',
      this.city = '',
      this.expiresAtUtc,
      this.expectedExitIpv4,
      this.protocolConfig,
      this.isForwarded = true});
  final ResourceKey key;
  final String name,
      customerRemark,
      protocol,
      host,
      username,
      password,
      countryCode,
      countryName,
      city;
  final int port;
  final DateTime? expiresAtUtc;

  /// Optional source metadata. Do not infer it from host or use it as a gate.
  final String? expectedExitIpv4;
  final ProtocolConfig? protocolConfig;
  final bool isForwarded;
}

final class Page<T> {
  Page({required List<T> items, required this.hasMore, this.nextCursor})
      : items = List.unmodifiable(items);
  final List<T> items;
  final bool hasMore;
  final String? nextCursor;
}

final class ResourceBrief {
  const ResourceBrief(
      {required this.key,
      required this.name,
      this.customerRemark = '',
      this.expiresAtUtc,
      this.owned = true,
      this.connectable = false});
  final ResourceKey key;
  final String name, customerRemark;
  final DateTime? expiresAtUtc;
  final bool owned, connectable;
}

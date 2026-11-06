import 'dart:convert';

import 'strict_json.dart';

/// Immutable, validated value for native protocolconfig.Config version 1.
/// The private canonical payload includes secrets. Only toJson exports it for
/// the authenticated connection boundary; never log or persist that payload.
final class ProtocolConfig {
  ProtocolConfig._(this.type, Map<String, Object?> value)
      : _canonical = jsonEncode(value);

  final String type;
  int get version => 1;
  final String _canonical;
  static const maxBytes = 16 * 1024;

  factory ProtocolConfig.parse(String text) {
    try {
      if (utf8.encode(text).length > maxBytes) _invalid();
      return ProtocolConfig.fromJson(decodeStrictJson(text));
    } catch (_) {
      _invalid();
    }
  }

  /// Use parse for raw JSON; a Map cannot retain duplicate-key evidence.
  factory ProtocolConfig.fromJson(Object? value) {
    try {
      if (value is! Map<String, dynamic> ||
          value['version'] is! int ||
          value['version'] != 1 ||
          value['type'] is! String ||
          utf8.encode(jsonEncode(value)).length > maxBytes) {
        _invalid();
      }
      final type = value['type'] as String;
      final section = type == 'https' ? 'http' : type;
      _object(value, {'version', 'type', 'tls', section});
      final options = _options(type, value[section]);
      const requiredTLS = {
        'vless',
        'trojan',
        'https',
        'hysteria2',
        'tuic',
        'anytls'
      };
      final hasTLS = value.containsKey('tls');
      if (requiredTLS.contains(type) && !hasTLS ||
          hasTLS && !requiredTLS.contains(type) && type != 'vmess') {
        _invalid();
      }
      return ProtocolConfig._(type, {
        'version': 1,
        'type': type,
        if (hasTLS) 'tls': _tls(type, value['tls']),
        section: options,
      });
    } catch (_) {
      _invalid();
    }
  }

  factory ProtocolConfig.legacySocks(
          {String username = '', String password = ''}) =>
      ProtocolConfig.fromJson({
        'version': 1,
        'type': 'socks5',
        'socks5': {'username': username, 'password': password},
      });

  /// Explicit configs may not inherit unrelated legacy authentication. Matching
  /// duplicate SOCKS credentials are allowed for additive server compatibility.
  bool matchesNode(
      {required String protocol, String username = '', String password = ''}) {
    if (protocol != type) return false;
    if (username.isEmpty && password.isEmpty) return true;
    if (type != 'socks5') return false;
    final options = toJson()['socks5'] as Map;
    return (options['username'] ?? '') == username &&
        (options['password'] ?? '') == password;
  }

  /// Returns a detached tree. Mutating it cannot change this validated value.
  Map<String, dynamic> toJson() =>
      jsonDecode(_canonical) as Map<String, dynamic>;
  @override
  bool operator ==(Object other) =>
      other is ProtocolConfig && _canonical == other._canonical;
  @override
  int get hashCode => _canonical.hashCode;
  @override
  String toString() => 'ProtocolConfig(version: 1, type: $type)';
}

Never _invalid() => throw const FormatException('INVALID_PROTOCOL_CONFIG');
Map<String, dynamic> _object(Object? value, Set<String> fields) {
  if (value is! Map<String, dynamic> || !value.keys.every(fields.contains))
    _invalid();
  return value;
}

String _text(Map<String, dynamic> value, String key, {bool optional = false}) {
  if (optional && !value.containsKey(key)) return '';
  final text = value[key];
  if (text is! String) _invalid();
  return text;
}

bool _secret(String value, [int limit = 1024]) =>
    value.isNotEmpty &&
    utf8.encode(value).length <= limit &&
    !value.contains(RegExp(r'[\x00\r\n]'));
bool _uuid(String value) =>
    RegExp(r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$')
        .hasMatch(value) &&
    value != '00000000-0000-0000-0000-000000000000';

Map<String, Object?> _options(String type, Object? raw) {
  final fields = switch (type) {
    'socks5' || 'http' || 'https' => {'username', 'password'},
    'shadowsocks' => {'method', 'password'},
    'vmess' => {'uuid', 'security', 'transport'},
    'vless' => {'uuid', 'transport', 'flow'},
    'trojan' => {'password', 'transport'},
    'hysteria2' => {'password', 'obfs'},
    'tuic' => {'uuid', 'password', 'congestionControl', 'udpRelayMode'},
    'anytls' => {'password'},
    _ => _invalid(),
  };
  final data = _object(raw, fields);
  if (type == 'socks5' || type == 'http' || type == 'https') {
    final u = _text(data, 'username', optional: true);
    final p = _text(data, 'password', optional: true);
    final limit = type == 'socks5' ? 255 : 1024;
    if (!(u.isEmpty && p.isEmpty || _secret(u, limit) && _secret(p, limit)) ||
        type != 'socks5' && u.contains(':')) _invalid();
    return {if (u.isNotEmpty) 'username': u, if (p.isNotEmpty) 'password': p};
  }
  final result = <String, Object?>{};
  if (fields.contains('uuid')) {
    final value = _text(data, 'uuid');
    if (!_uuid(value)) _invalid();
    result['uuid'] = value;
  }
  if (fields.contains('password')) {
    final value = _text(data, 'password');
    if (!_secret(value)) _invalid();
    result['password'] = value;
  }
  if (fields.contains('transport')) {
    if (_text(data, 'transport') != 'tcp') _invalid();
    result['transport'] = 'tcp';
  }
  switch (type) {
    case 'shadowsocks':
      final method = _text(data, 'method');
      const aead = {'aes-128-gcm', 'aes-256-gcm', 'chacha20-ietf-poly1305'};
      const ss2022 = {
        '2022-blake3-aes-128-gcm',
        '2022-blake3-aes-256-gcm',
        '2022-blake3-chacha20-poly1305'
      };
      if (!aead.contains(method)) {
        if (!ss2022.contains(method)) _invalid();
        final password = result['password'] as String;
        final decoded = base64.decode(password);
        final size = method == '2022-blake3-aes-128-gcm' ? 16 : 32;
        if (decoded.length != size || base64.encode(decoded) != password)
          _invalid();
      }
      result['method'] = method;
    case 'vmess':
      final security = _text(data, 'security');
      if (!{'aes-128-gcm', 'chacha20-poly1305'}.contains(security)) _invalid();
      result['security'] = security;
    case 'vless':
      final flow = _text(data, 'flow', optional: true);
      if (flow != '' && flow != 'xtls-rprx-vision') _invalid();
      if (flow.isNotEmpty) result['flow'] = flow;
    case 'hysteria2':
      if (data.containsKey('obfs')) {
        final obfs = _object(data['obfs'], {'type', 'password'});
        final password = _text(obfs, 'password');
        if (_text(obfs, 'type') != 'salamander' || !_secret(password))
          _invalid();
        result['obfs'] = {'type': 'salamander', 'password': password};
      }
    case 'tuic':
      final congestion = _text(data, 'congestionControl');
      final relay = _text(data, 'udpRelayMode');
      if (!{'cubic', 'new_reno', 'bbr'}.contains(congestion) ||
          !{'native', 'quic'}.contains(relay)) _invalid();
      result['congestionControl'] = congestion;
      result['udpRelayMode'] = relay;
  }
  return result;
}

Map<String, Object?> _tls(String type, Object? raw) {
  final data = _object(raw, {'serverName', 'alpn'});
  final name = _text(data, 'serverName');
  if (!_serverName(name)) _invalid();
  final alpn = <String>[];
  if (data.containsKey('alpn')) {
    final values = data['alpn'];
    if (values is! List || values.length > 8) _invalid();
    for (final item in values) {
      if (item is! String ||
          item.isEmpty ||
          item.length > 255 ||
          item.codeUnits.any((c) => c < 33 || c > 126) ||
          alpn.contains(item) ||
          type == 'https' && item != 'http/1.1' ||
          type == 'hysteria2' && item != 'h3') _invalid();
      alpn.add(item);
    }
  }
  return {'serverName': name, if (alpn.isNotEmpty) 'alpn': alpn};
}

bool _serverName(String value) {
  if (value.isEmpty || value.length > 253) return false;
  final labels = value.split('.');
  if (labels.any((s) =>
      s.isEmpty ||
      s.length > 63 ||
      !RegExp(r'^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$').hasMatch(s)))
    return false;
  // Match netip.ParseAddr + IsGlobalUnicast, not observed public-IP validation.
  // Private IPv4 certificate identities are valid; the dial address is checked
  // separately by native. Strings not parsed as IPv4 follow the DNS-label rule.
  if (labels.length == 4 &&
      labels.every((s) =>
          RegExp(r'^(0|[1-9][0-9]{0,2})$').hasMatch(s) &&
          int.parse(s) <= 255)) {
    final p = labels.map(int.parse).toList();
    return value != '0.0.0.0' &&
        value != '255.255.255.255' &&
        p[0] != 127 &&
        !(p[0] == 169 && p[1] == 254) &&
        !(p[0] >= 224 && p[0] <= 239);
  }
  return true;
}

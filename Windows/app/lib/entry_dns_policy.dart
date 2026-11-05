import 'dart:io';

/// Entry-domain resolution only. These values never modify system/business DNS.
class EntryDnsCandidate {
  factory EntryDnsCandidate({
    required String address,
    int port = 53,
    String transport = 'udp',
  }) {
    final parsed = InternetAddress.tryParse(address);
    if (parsed == null || address.contains('%') || address.trim() != address) {
      throw const FormatException('DNS 地址必须是 IPv4 或 IPv6 数字地址，不支持域名或 URL。');
    }
    final bytes = parsed.rawAddress;
    final mappedIpv4 =
        bytes.length == 16 &&
        bytes.take(10).every((byte) => byte == 0) &&
        bytes[10] == 0xff &&
        bytes[11] == 0xff;
    final ipv6LinkLocal =
        bytes.length == 16 && bytes[0] == 0xfe && bytes[1] & 0xc0 == 0x80;
    if (parsed.isLoopback ||
        parsed.isMulticast ||
        bytes.every((byte) => byte == 0) ||
        mappedIpv4 ||
        ipv6LinkLocal) {
      throw const FormatException(
        '不支持回环、未指定、组播、IPv4 映射 IPv6 或 IPv6 链路本地地址。'
        '可使用私有 IPv4 路由器地址，或无区域标识的全局 / ULA IPv6。',
      );
    }
    if (port < 1 || port > 65535) {
      throw const FormatException('DNS 端口必须为 1 至 65535。');
    }
    if (transport != 'udp' && transport != 'tcp') {
      throw const FormatException('DNS 传输协议仅支持 UDP 或 TCP。');
    }
    return EntryDnsCandidate._(parsed.address, port, transport);
  }
  const EntryDnsCandidate._(this.address, this.port, this.transport);
  final String address, transport;
  final int port;

  factory EntryDnsCandidate.fromJson(Object? value) {
    if (value is! Map<String, dynamic> ||
        value.length != 3 ||
        value['address'] is! String ||
        value['port'] is! int ||
        value['transport'] is! String) {
      throw const FormatException('入口 DNS 服务器配置无效。');
    }
    return EntryDnsCandidate(
      address: value['address'] as String,
      port: value['port'] as int,
      transport: value['transport'] as String,
    );
  }
  Map<String, Object> toJson() => {
    'address': address,
    'port': port,
    'transport': transport,
  };
}

class EntryDnsPolicy {
  const EntryDnsPolicy.auto() : mode = 'auto', candidates = const [];
  EntryDnsPolicy._(List<EntryDnsCandidate> values)
    : mode = 'custom',
      candidates = List.unmodifiable(values);
  static const maxCandidates = 8;
  final String mode;
  final List<EntryDnsCandidate> candidates;

  factory EntryDnsPolicy.custom(List<EntryDnsCandidate> values) {
    if (values.isEmpty || values.length > maxCandidates) {
      throw const FormatException('自定义入口 DNS 必须包含 1 至 8 个服务器。');
    }
    return EntryDnsPolicy._(values);
  }
  factory EntryDnsPolicy.fromJson(Object? value) {
    if (value is! Map<String, dynamic> ||
        value.length != 2 ||
        value['candidates'] is! List) {
      throw const FormatException('入口 DNS 策略无效。');
    }
    final values = value['candidates'] as List;
    if (value['mode'] == 'auto' && values.isEmpty) {
      return const EntryDnsPolicy.auto();
    }
    if (value['mode'] != 'custom' || values.length > maxCandidates) {
      throw const FormatException('入口 DNS 模式或服务器数量无效。');
    }
    return EntryDnsPolicy.custom(
      values.map(EntryDnsCandidate.fromJson).toList(),
    );
  }
  Map<String, Object> toJson() => {
    'mode': mode,
    'candidates': candidates.map((value) => value.toJson()).toList(),
  };
}

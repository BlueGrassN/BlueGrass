import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';

class ResourceItem {
  const ResourceItem({
    required this.key,
    required this.name,
    this.remark = '',
    this.expiresAtUtc,
    required this.eligible,
    required this.status,
    this.protocol,
    this.location = '',
  });
  final ResourceKey key;
  final bool eligible;
  final String name, remark, status, location;
  final DateTime? expiresAtUtc;
  final String? protocol;

  factory ResourceItem.fromNode(NodeRecord node, DateTime now) {
    final result = prepareProfile(node, now);
    return ResourceItem(
      key: node.key,
      name: node.name,
      remark: node.customerRemark,
      expiresAtUtc: node.expiresAtUtc,
      eligible: result is Ready,
      status: result is Ready
          ? '可选择'
          : result is Rejected && result.error.code == 'ELIG-003'
          ? '已到期'
          : result is Rejected && result.error.code == 'ELIG-008'
          ? 'Xray 暂不支持此协议'
          : '暂不可用',
      protocol: node.protocol,
      location: [
        node.countryName,
        node.city,
      ].where((v) => v.isNotEmpty).join(' · '),
    );
  }

  factory ResourceItem.fromOwned(OwnedResourceRecord record) => ResourceItem(
    key: record.summary.key,
    name: record.summary.name,
    remark: record.summary.customerRemark,
    expiresAtUtc: record.summary.expiresAtUtc,
    // Owned-resource summaries contain no validated connection configuration.
    eligible: false,
    status: switch (record.status) {
      'active' => '有效',
      'expired' => '已到期',
      'disabled' => '已停用',
      'suspended' => '已暂停',
      _ => '待确认',
    },
  );
}

String formatExpiry(DateTime? utc) {
  if (utc == null) return '未指定到期时间';
  final t = utc.toUtc().add(const Duration(hours: 8));
  String two(int value) => value.toString().padLeft(2, '0');
  return '${t.year}-${two(t.month)}-${two(t.day)} ${two(t.hour)}:${two(t.minute)}（北京时间）';
}

String friendlyError(Object failure) {
  if (failure is! ApiFailure) return '操作未完成，请重试。';
  return switch (failure.code) {
    'invalid_credentials' => '账号或密码不正确，请重新输入。',
    'validation_failed' => '请检查账号和密码后重试。',
    'account_disabled' || 'account_unavailable' => '账号暂不可用，请联系服务方。',
    'storage_error' => '无法访问安全存储，请重试。登录信息不会以明文保存。',
    'invalid_session' || 'environment_mismatch' => '本机会话无法恢复，请清除后重新登录。',
    'network_error' || 'timeout' => '网络连接失败，请检查网络后重试。',
    'rate_limited' || 'retry_later' => '请求较频繁，请稍后重试。',
    'capability_unavailable' => '当前服务暂不支持此资源视图。',
    'invalid_response' || 'pagination_invalid' => '资源同步未完成，请刷新重试。',
    'logout_remote_unconfirmed' => '已清除本机登录信息，服务端退出确认暂未完成。',
    'not_authenticated' ||
    'session_expired' ||
    'session_revoked' ||
    'credentials_changed' ||
    'invalid_refresh_token' ||
    'refresh_reuse_detected' ||
    'invalid_token' => '登录已失效，请重新登录。',
    _ => failure.status == 429 ? '请求较频繁，请稍后重试。' : '服务暂不可用，请稍后重试。',
  };
}

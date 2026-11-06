import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:flutter/material.dart';

import 'ui/kit.dart';
import 'ui/resource_kit.dart';

export 'ui/resource_kit.dart';

String formatExpiry(DateTime? utc) {
  if (utc == null) return '未指定到期';
  final t = utc.toUtc().add(const Duration(hours: 8));
  String two(int n) => n.toString().padLeft(2, '0');
  return '${t.year.toString().padLeft(4, '0')}-${two(t.month)}-${two(t.day)} '
      '${two(t.hour)}:${two(t.minute)}:${two(t.second)}（北京时间 UTC+8）';
}

String? resourceUnavailableReason(domain.NodeRecord node, DateTime now) {
  final prepared = domain.prepareProfile(node, now);
  final unavailable = prepared is domain.Rejected
      ? switch (prepared.error.code) {
          'SYNC-003' => 'SYNC-003 · 资源信息无效，请刷新后重试。',
          'ELIG-001' => 'ELIG-001 · 资源未提供中转，暂时无法连接。',
          'ELIG-003' => 'ELIG-003 · 资源已到期，请在官网续费后刷新。',
          'ELIG-005' => 'ELIG-005 · 入口地址或端口无效，请刷新资源。',
          'ELIG-007' => 'ELIG-007 · 协议配置不完整或不受支持，暂时无法连接。',
          'ELIG-008' => '当前 Xray 内核不支持此协议，请选择其他资源。',
          _ => '${prepared.error.code} · 资源当前不能连接，请刷新资源。',
        }
      : null;
  return unavailable;
}

/// Commerce links are configured public pages only, never decorated with
/// account identifiers, credentials, return tokens, or transaction parameters.
Uri? publicWebsiteUri(String value) {
  final uri = Uri.tryParse(value);
  if (uri == null ||
      uri.scheme != 'https' ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasQuery ||
      uri.hasFragment ||
      value.contains(RegExp(r'[\x00-\x20\\]'))) {
    return null;
  }
  return uri;
}

class ResourceDetails extends StatelessWidget {
  const ResourceDetails({
    super.key,
    required this.node,
    required this.openWebsite,
    this.actualExitIp,
  });
  final domain.NodeRecord node;
  final VoidCallback openWebsite;
  final String? actualExitIp;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return ResAlert(
      title: node.name,
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          spacing: 12,
          children: [
            ResFieldGroup(
              children: [
                ResField(
                  '资源类型 / 编号',
                  '${node.key.kind} / ${node.key.sourceId}',
                ),
                if (node.customerRemark.isNotEmpty)
                  ResField('客户备注', node.customerRemark),
                ResField(
                  '地区',
                  [
                    node.countryName,
                    node.city,
                  ].where((v) => v.isNotEmpty).join(' / '),
                ),
                ResField('协议', node.protocol.toUpperCase()),
              ],
            ),
            ResFieldGroup(
              children: [
                ResField(
                  '中转状态',
                  node.isForwarded ? '已提供中转配置；连接前重新核验资格' : '未提供中转，当前不可连接',
                ),
                ResField(
                  '节点出口 IP',
                  actualExitIp ?? '暂未获取',
                  note: '连接后通过所选节点检测 HTTPS 出口；暂未获取不影响连接，可在“诊断”中重试。',
                ),
                ResField('客户到期时间', formatExpiry(node.expiresAtUtc)),
              ],
            ),
            ResFieldGroup(
              children: [
                ResField(
                  '传输保护',
                  node.protocol.toLowerCase() == 'socks5'
                      ? 'SOCKS5 本身不加密认证信息或代理传输。HTTPS 网站内容仍受其 TLS 保护；未核实的外层加密不作保证。'
                      : '以服务端下发的实际传输配置与验证结果为准；协议名称本身不证明链路已加密。',
                ),
              ],
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4),
              child: Text(
                '本页只读。购买、续费与业务变更在官网完成；返回后刷新资源状态。',
                style: AppTextStyle.caption1.of(
                  context,
                  color: s.onSurfaceVariant,
                ),
              ),
            ),
          ],
        ),
      ),
      actions: [
        ResAlertButton(
          label: '前往官网',
          icon: Icons.open_in_new_rounded,
          onPressed: openWebsite,
        ),
        ResAlertButton(
          label: '关闭',
          isDefault: true,
          onPressed: () => Navigator.pop(context),
        ),
      ],
    );
  }
}

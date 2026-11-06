import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../presentation/settings_content.dart';
import '../app_controller.dart';
import 'components.dart';
import 'vpn_presentation.dart';

class DiagnosticsScreen extends StatefulWidget {
  const DiagnosticsScreen({super.key, required this.controller});
  final AppController controller;

  @override
  State<DiagnosticsScreen> createState() => _DiagnosticsScreenState();
}

class _DiagnosticsScreenState extends State<DiagnosticsScreen> {
  bool _checking = false;

  Future<void> _check() async {
    setState(() => _checking = true);
    try {
      await widget.controller.resumeVpn();
      if (!mounted) return;
      await widget.controller.refreshVpnEligibility();
    } finally {
      if (mounted) setState(() => _checking = false);
    }
  }

  Future<void> _copy() async {
    final c = widget.controller;
    // Deliberate allowlist: no account/resource IDs, names, endpoints, config,
    // runtime/request identifiers or raw platform errors in the export.
    final text = const JsonEncoder.withIndent('  ').convert({
      'platform': 'Android',
      'vpnAvailable': c.vpnAvailable,
      'state': c.vpnSnapshot?.state.name,
      'tunActive': c.vpnSnapshot?.tunActive,
      'observationState': c.observationState.name,
      'errorCode': c.vpnSnapshot?.errorCode,
    });
    try {
      await Clipboard.setData(ClipboardData(text: text));
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('已复制脱敏诊断信息')));
      }
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('复制失败，请重试。')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.controller;
    final snapshot = c.vpnSnapshot;
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    return ListView(
      key: const PageStorageKey('diagnostics-scroll'),
      padding: EdgeInsets.fromLTRB(
        16,
        8,
        16,
        24 + MediaQuery.paddingOf(context).bottom,
      ),
      children: [
        SurfaceCard(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: DiagnosticsActions(
              description: '刷新 VPN 状态，检查当前资源是否可用。',
              checking: _checking,
              onRun: _checking || c.authBusy || c.vpnBusy || !c.vpnAvailable
                  ? null
                  : _check,
              onCopy: snapshot == null ? null : _copy,
            ),
          ),
        ),
        const SizedBox(height: 12),
        SurfaceCard(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const SectionTitle('本地状态', Icons.shield_outlined),
                const SizedBox(height: 16),
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: colors.surfaceContainer,
                    borderRadius: BorderRadius.circular(20),
                  ),
                  child: Column(
                    children: [
                      _DiagnosticField(
                        icon: Icons.power_settings_new,
                        label: '连接状态',
                        value: snapshot == null
                            ? '暂未获取'
                            : vpnStateLabel(c.vpnState),
                      ),
                      const SizedBox(height: 16),
                      _DiagnosticField(
                        icon: Icons.shield_outlined,
                        label: 'VPN 保护通道',
                        value: snapshot == null
                            ? '暂未获取'
                            : snapshot.tunActive
                            ? '已保留'
                            : '未启用',
                      ),
                      const SizedBox(height: 16),
                      _DiagnosticField(
                        icon: Icons.alt_route,
                        label: '分流模式',
                        value: c.routingMode == 'bypass_cn' ? '国内外分流' : '全局代理',
                      ),
                      if (snapshot?.errorCode != null) ...[
                        const SizedBox(height: 16),
                        _DiagnosticField(
                          icon: Icons.info_outline,
                          label: '状态代码',
                          value: snapshot!.errorCode!,
                        ),
                      ],
                    ],
                  ),
                ),
                const SizedBox(height: 12),
                Text(
                  c.vpnAvailable ? vpnStateDetail(c) : 'VPN 暂不可用',
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: colors.onSurfaceVariant,
                    height: 1.5,
                  ),
                ),
              ],
            ),
          ),
        ),
        if (c.vpnError != null) ...[
          const SizedBox(height: 12),
          NoticeCard(c.vpnError!, error: true),
        ],
        if (c.authError != null) ...[
          const SizedBox(height: 12),
          NoticeCard(c.authError!, error: true),
        ],
        const SizedBox(height: 12),
        SurfaceCard(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const SectionTitle('出口检测', Icons.public),
                const SizedBox(height: 16),
                Container(
                  padding: const EdgeInsets.all(16),
                  decoration: BoxDecoration(
                    color: colors.surfaceContainer,
                    borderRadius: BorderRadius.circular(20),
                  ),
                  child: DetailField('节点出口 IP', c.observedExitIp ?? '暂未获取'),
                ),
                const SizedBox(height: 12),
                Text(
                  exitObservationDetail(c.observationState),
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: colors.onSurfaceVariant,
                    height: 1.5,
                  ),
                ),
                const SizedBox(height: 16),
                OutlinedButton.icon(
                  key: const Key('diagnostic-observe-exit'),
                  onPressed: !_checking && canObserveExit(c)
                      ? c.observeExit
                      : null,
                  icon: const Icon(Icons.refresh),
                  label: const Text('检测出口'),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _DiagnosticField extends StatelessWidget {
  const _DiagnosticField({
    required this.icon,
    required this.label,
    required this.value,
  });

  final IconData icon;
  final String label, value;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Row(
      children: [
        Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: colors.primaryContainer,
            borderRadius: BorderRadius.circular(14),
          ),
          child: Icon(icon, size: 20, color: colors.onPrimaryContainer),
        ),
        const SizedBox(width: 12),
        Expanded(child: DetailField(label, value)),
      ],
    );
  }
}

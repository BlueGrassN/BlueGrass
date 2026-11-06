import 'package:flutter/material.dart';

import '../../presentation/connection_content.dart';
import '../../../ui/start_button.dart';
import '../app_controller.dart';
import '../platform/vpn_client.dart';
import '../resource_presentation.dart';
import 'components.dart';
import 'vpn_presentation.dart';
import 'routing_mode_selector.dart';
import 'traffic_card.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({
    super.key,
    required this.controller,
    required this.chooseResource,
    required this.openDiagnostics,
    this.active = true,
  });
  final AppController controller;
  final bool active;
  final VoidCallback chooseResource, openDiagnostics;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final selected = controller.selected;
    final nativeKey = controller.vpnSnapshot?.resourceKey;
    final active = controller.activeResource?.key.stableId == nativeKey
        ? controller.activeResource
        : controller.resources
              .where((item) => item.key.stableId == nativeKey)
              .firstOrNull;
    final resource = nativeKey == null ? selected : active;
    final state = controller.vpnState;
    final sameResource = nativeKey == selected?.key.stableId;
    final needsReconnect =
        state == VpnState.paused ||
        state == VpnState.error ||
        (state == VpnState.connected && !sameResource);
    final name =
        resource?.name ??
        (nativeKey != null
            ? '连接资源待同步'
            : controller.loading
            ? '正在同步资源'
            : '选择连接资源');
    return RefreshIndicator(
      onRefresh: controller.refresh,
      child: ListView(
        key: const PageStorageKey('home-scroll'),
        padding: EdgeInsets.fromLTRB(
          16,
          10,
          16,
          24 + MediaQuery.paddingOf(context).bottom,
        ),
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          _DashboardCard(
            accented: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Wrap(
                  crossAxisAlignment: WrapCrossAlignment.center,
                  spacing: 10,
                  runSpacing: 8,
                  children: [
                    Icon(
                      Icons.monitor_heart_outlined,
                      color: colors.onSurfaceVariant,
                      size: 18,
                    ),
                    Text(
                      '连接状态',
                      style: TextStyle(
                        color: colors.onSurfaceVariant,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    _StatusBadge(state: state),
                  ],
                ),
                const SizedBox(height: 12),
                Text(
                  name,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.titleSmall
                      ?.copyWith(color: colors.onSurfaceVariant),
                ),
                const SizedBox(height: 16),
                LayoutBuilder(
                  builder: (context, constraints) {
                    final details = ConnectionContent(
                      large: true,
                      data: ConnectionViewData(
                        exitIp: controller.observedExitIp,
                        detail:
                            controller.observationState ==
                                VpnObservationState.failed
                            ? exitObservationDetail(controller.observationState)
                            : vpnStateDetail(controller),
                      ),
                    );
                    final power = _PowerControl(controller: controller);
                    if (constraints.maxWidth < 260 ||
                        MediaQuery.textScalerOf(context).scale(14) > 21) {
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          details,
                          const SizedBox(height: 16),
                          Align(alignment: Alignment.centerRight, child: power),
                        ],
                      );
                    }
                    return Row(
                      children: [
                        Expanded(child: details),
                        const SizedBox(width: 12),
                        power,
                      ],
                    );
                  },
                ),
                if (!controller.vpnAvailable) ...[
                  const SizedBox(height: 12),
                  const Text('VPN 暂不可用'),
                ],
                if (controller.canDisconnect &&
                    (state == VpnState.connecting ||
                        state == VpnState.idle)) ...[
                  const SizedBox(height: 12),
                  OutlinedButton(
                    key: const Key('disconnect'),
                    onPressed: controller.disconnect,
                    child: const Text('取消连接'),
                  ),
                ],
                if (needsReconnect) ...[
                  const SizedBox(height: 12),
                  if (selected != null && !sameResource && nativeKey != null)
                    Text('待切换资源：${selected.name}'),
                  const SizedBox(height: 8),
                  FilledButton.icon(
                    key: const Key('connect'),
                    onPressed: controller.canConnect
                        ? controller.connect
                        : null,
                    icon: const Icon(Icons.refresh_rounded),
                    label: Text(state == VpnState.connected ? '切换并连接' : '重新连接'),
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(height: 16),
          TrafficCard(model: controller.traffic, active: this.active),
          const SizedBox(height: 16),
          _DashboardCard(child: RoutingModeSelector(controller: controller)),
          const SizedBox(height: 16),
          _DashboardCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    const Expanded(
                      child: SectionTitle('当前资源', Icons.layers_outlined),
                    ),
                    IconButton(
                      tooltip: '选择资源',
                      onPressed: chooseResource,
                      icon: const Icon(Icons.chevron_right_rounded),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                InkWell(
                  key: const Key('choose-resource'),
                  onTap: chooseResource,
                  borderRadius: BorderRadius.circular(22),
                  child: Ink(
                    padding: const EdgeInsets.all(16),
                    decoration: BoxDecoration(
                      color: colors.surfaceContainer,
                      borderRadius: BorderRadius.circular(22),
                    ),
                    child: Row(
                      children: [
                        Container(
                          padding: const EdgeInsets.all(10),
                          decoration: BoxDecoration(
                            color: colors.primaryContainer,
                            borderRadius: BorderRadius.circular(16),
                          ),
                          child: Icon(
                            Icons.public_rounded,
                            color: colors.primary,
                          ),
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                '查看连接资源',
                                style: Theme.of(context).textTheme.titleMedium,
                              ),
                              const SizedBox(height: 4),
                              Text(
                                resource == null
                                    ? (nativeKey == null
                                          ? '轻触选择可用资源'
                                          : '资源详情暂未同步')
                                    : [
                                        resource.location,
                                        resource.protocol?.toUpperCase() ?? '',
                                      ].where((v) => v.isNotEmpty).join(' · '),
                                style: TextStyle(
                                  color: colors.onSurfaceVariant,
                                  fontSize: 12,
                                ),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                if (resource != null) ...[
                  const SizedBox(height: 16),
                  Text(
                    '到期时间 · 北京时间',
                    style: TextStyle(
                      color: colors.onSurfaceVariant,
                      fontSize: 12,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    formatExpiry(resource.expiresAtUtc),
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                ],
              ],
            ),
          ),
          for (final (message, retry, error)
              in <(String?, VoidCallback?, bool)>[
                (controller.settingsError, null, true),
                (controller.vpnError, controller.resumeVpn, true),
                (controller.authError, null, true),
                (controller.resourceError, controller.refresh, true),
                (controller.notice, null, false),
              ])
            if (message != null) ...[
              const SizedBox(height: 12),
              NoticeCard(message, onRetry: retry, error: error),
            ],
          if (controller.loading)
            const Padding(
              padding: EdgeInsets.only(top: 12),
              child: LinearProgressIndicator(),
            ),
          const SizedBox(height: 12),
          _DashboardCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const SectionTitle('连接检查', Icons.verified_user_outlined),
                const SizedBox(height: 14),
                Text(
                  controller.observationState == VpnObservationState.failed
                      ? '可重新检测出口，或前往诊断查看连接详情。'
                      : exitObservationDetail(controller.observationState),
                  style: TextStyle(color: colors.onSurfaceVariant),
                ),
                const SizedBox(height: 14),
                Wrap(
                  spacing: 12,
                  runSpacing: 8,
                  children: [
                    OutlinedButton.icon(
                      key: const Key('observe-exit'),
                      onPressed: canObserveExit(controller)
                          ? controller.observeExit
                          : null,
                      icon: const Icon(Icons.public_rounded),
                      label: const Text('检测出口'),
                    ),
                    TextButton(
                      onPressed: openDiagnostics,
                      child: const Text('前往诊断'),
                    ),
                  ],
                ),
              ],
            ),
          ),
          const SizedBox(height: 20),
          Text(
            'BlueGrass · 首次连接需授予系统 VPN 权限',
            textAlign: TextAlign.center,
            style: TextStyle(color: colors.onSurfaceVariant, fontSize: 12),
          ),
        ],
      ),
    );
  }
}

class _DashboardCard extends StatelessWidget {
  const _DashboardCard({required this.child, this.accented = false});
  final Widget child;
  final bool accented;
  @override
  Widget build(BuildContext context) => SurfaceCard(
    shape: RoundedSuperellipseBorder(borderRadius: BorderRadius.circular(34)),
    child: Stack(
      children: [
        if (accented)
          Positioned(
            right: -76,
            top: -62,
            width: 242,
            height: 242,
            child: IgnorePointer(
              child: Transform.rotate(
                angle: .25,
                child: DecoratedBox(
                  decoration: ShapeDecoration(
                    color: Color.alphaBlend(
                      Theme.of(context).colorScheme.primary
                          .withValues(alpha: .085),
                      Theme.of(context).colorScheme.surface,
                    ),
                    shape: const StarBorder(
                      points: 10,
                      innerRadiusRatio: .88,
                      pointRounding: .5,
                      valleyRounding: .5,
                    ),
                  ),
                ),
              ),
            ),
          ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
          child: child,
        ),
      ],
    ),
  );
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.state});
  final VpnState state;
  @override
  Widget build(BuildContext context) => ConnectionStatusChip(
    vpnStateLabel(state),
    dot: true,
    tone: state == VpnState.connected
        ? ConnectionTone.ok
        : state == VpnState.paused || state == VpnState.error
        ? ConnectionTone.warn
        : ConnectionTone.idle,
  );
}

class _PowerControl extends StatelessWidget {
  const _PowerControl({required this.controller});
  final AppController controller;
  @override
  Widget build(BuildContext context) {
    final state = controller.vpnState;
    final stopping = state != VpnState.idle && state != VpnState.connecting;
    final label = stopping
        ? '断开连接'
        : controller.vpnBusy
        ? '正在处理…'
        : '连接';
    return StartToggleButton(
      buttonKey: Key(stopping ? 'disconnect' : 'connect'),
      running: state != VpnState.idle,
      size: 88,
      label: label,
      icon: stopping ? Icons.pause_rounded : Icons.power_settings_new_rounded,
      onPressed: stopping
          ? (controller.canDisconnect ? controller.disconnect : null)
          : (controller.canConnect ? controller.connect : null),
    );
  }
}

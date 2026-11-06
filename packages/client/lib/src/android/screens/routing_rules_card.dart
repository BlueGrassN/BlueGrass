import 'dart:async';

import 'package:flutter/material.dart';

import '../resource_presentation.dart';
import '../routing_rules.dart';
import 'components.dart';

class RoutingRulesCard extends StatefulWidget {
  const RoutingRulesCard({
    super.key,
    required this.model,
    required this.active,
    required this.vpnActive,
  });

  final RoutingRulesModel model;
  final bool active, vpnActive;

  @override
  State<RoutingRulesCard> createState() => _RoutingRulesCardState();
}

class _RoutingRulesCardState extends State<RoutingRulesCard>
    with WidgetsBindingObserver {
  Timer? _timer;
  int _epoch = 0;
  bool get _visible =>
      mounted &&
      widget.active &&
      (WidgetsBinding.instance.lifecycleState == null ||
          WidgetsBinding.instance.lifecycleState == AppLifecycleState.resumed);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _restart();
  }

  @override
  void didUpdateWidget(RoutingRulesCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.active != widget.active || oldWidget.model != widget.model) {
      _restart();
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) => _restart();

  void _restart() {
    _timer?.cancel();
    final epoch = ++_epoch;
    if (_visible) unawaited(_poll(epoch));
  }

  Future<void> _poll(int epoch) async {
    await widget.model.refresh();
    _schedule(epoch);
  }

  void _schedule(int epoch) {
    if (!_visible || epoch != _epoch || !widget.model.available) return;
    _timer = Timer(
      widget.model.updating
          ? const Duration(seconds: 2)
          : const Duration(seconds: 30),
      () => unawaited(_poll(epoch)),
    );
  }

  Future<void> _update() async {
    final update = widget.model.update();
    // Observe native progress while the asynchronous update call is pending.
    _restart();
    await update;
    if (mounted) {
      _timer?.cancel();
      // Keep the completed result/error visible; an immediate status read can
      // erase a channel failure before the user has a chance to see it.
      _schedule(++_epoch);
    }
  }

  @override
  void dispose() {
    _epoch++;
    _timer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.model,
    builder: (context, _) {
      final model = widget.model;
      final status = model.status;
      final theme = Theme.of(context);
      final colors = theme.colorScheme;
      return SurfaceCard(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      color: colors.primaryContainer,
                      borderRadius: BorderRadius.circular(14),
                    ),
                    child: Icon(
                      Icons.update,
                      size: 20,
                      color: colors.onPrimaryContainer,
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      '规则自动更新',
                      style: theme.textTheme.titleSmall?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              Text('默认每天自动检查', style: theme.textTheme.titleSmall),
              const SizedBox(height: 4),
              Text(
                '联网时后台检查，系统可能延后执行。',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: colors.onSurfaceVariant,
                  height: 1.5,
                ),
              ),
              const SizedBox(height: 16),
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: colors.surfaceContainer,
                  borderRadius: BorderRadius.circular(20),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    DetailField('规则版本', status?.version ?? '暂未获取'),
                    if (status != null) ...[
                      const SizedBox(height: 8),
                      Align(
                        alignment: AlignmentDirectional.centerStart,
                        child: Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 10,
                            vertical: 5,
                          ),
                          decoration: BoxDecoration(
                            color: colors.primaryContainer,
                            borderRadius: BorderRadius.circular(12),
                          ),
                          child: Text(
                            status.source == 'downloaded' ? '已下载规则' : '内置规则',
                            style: theme.textTheme.labelMedium?.copyWith(
                              color: colors.onPrimaryContainer,
                            ),
                          ),
                        ),
                      ),
                    ],
                    const SizedBox(height: 16),
                    DetailField(
                      '最近检查',
                      status?.checkedAt == null
                          ? (status == null ? '暂未获取' : '尚未检查')
                          : formatExpiry(status!.checkedAt),
                    ),
                    const SizedBox(height: 16),
                    DetailField(
                      '最近更新',
                      status?.updatedAt == null
                          ? (status == null ? '暂未获取' : '尚未更新')
                          : formatExpiry(status!.updatedAt),
                    ),
                  ],
                ),
              ),
              if (widget.vpnActive) ...[
                const SizedBox(height: 12),
                const NoticeCard('VPN 连接期间仍使用当前规则，新规则将在下次连接时生效，不会自动断开。'),
              ],
              if (model.error != null) ...[
                const SizedBox(height: 12),
                NoticeCard(model.error!, error: true),
              ],
              const SizedBox(height: 12),
              OutlinedButton.icon(
                key: const Key('routing-rules-update'),
                onPressed: model.canUpdate ? _update : null,
                icon: model.loading || model.updating
                    ? const SizedBox.square(
                        dimension: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.refresh),
                label: Text(
                  model.updating
                      ? '正在更新…'
                      : model.loading
                      ? '读取状态中…'
                      : '立即更新',
                ),
              ),
            ],
          ),
        ),
      );
    },
  );
}

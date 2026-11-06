import 'package:flutter/material.dart';

import '../../presentation/traffic_content.dart';
import '../traffic.dart';
import 'components.dart';

class TrafficCard extends StatefulWidget {
  const TrafficCard({super.key, required this.model, required this.active});
  final TrafficModel model;
  final bool active;
  @override
  State<TrafficCard> createState() => _TrafficCardState();
}

class _TrafficCardState extends State<TrafficCard> with WidgetsBindingObserver {
  void _visibility() => widget.model.setVisible(
    widget.active &&
        (WidgetsBinding.instance.lifecycleState == null ||
            WidgetsBinding.instance.lifecycleState ==
                AppLifecycleState.resumed),
  );
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _visibility();
  }

  @override
  void didUpdateWidget(TrafficCard old) {
    super.didUpdateWidget(old);
    if (old.model != widget.model) old.model.setVisible(false);
    _visibility();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) => _visibility();
  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    widget.model.setVisible(false);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.model,
    builder: (context, _) {
      final m = widget.model;
      final theme = Theme.of(context), colors = theme.colorScheme;
      return SurfaceCard(
        shape: RoundedSuperellipseBorder(
          borderRadius: BorderRadius.circular(34),
        ),
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SectionTitle('流量统计', Icons.speed_rounded),
              const SizedBox(height: 16),
              LayoutBuilder(
                builder: (context, constraints) {
                  final stacked =
                      constraints.maxWidth < 270 ||
                      MediaQuery.textScalerOf(context).scale(14) > 20;
                  final speed = [
                    _Metric(
                      label: '上传速度',
                      icon: Icons.arrow_upward_rounded,
                      amount: formatTraffic(m.uploadRate, rate: true),
                      speed: true,
                      color: colors.onSurfaceVariant,
                    ),
                    _Metric(
                      label: '下载速度',
                      icon: Icons.arrow_downward_rounded,
                      amount: formatTraffic(m.downloadRate, rate: true),
                      speed: true,
                      color: colors.primary,
                    ),
                  ];
                  return stacked
                      ? Column(
                          children: [
                            speed[0],
                            const SizedBox(height: 10),
                            speed[1],
                          ],
                        )
                      : Row(
                          children: [
                            Expanded(child: speed[0]),
                            const SizedBox(width: 10),
                            Expanded(child: speed[1]),
                          ],
                        );
                },
              ),
              const SizedBox(height: 18),
              LayoutBuilder(
                builder: (context, constraints) {
                  final stacked =
                      constraints.maxWidth < 300 ||
                      MediaQuery.textScalerOf(context).scale(14) > 19;
                  final totals = [
                    _Metric(
                      label: '总流量',
                      amount: formatTraffic(m.totalBytes?.toDouble()),
                      color: colors.primary,
                    ),
                    _Metric(
                      label: '总上传',
                      amount: formatTraffic(m.uploadBytes?.toDouble()),
                      color: colors.onSurface,
                    ),
                    _Metric(
                      label: '总下载',
                      amount: formatTraffic(m.downloadBytes?.toDouble()),
                      color: colors.onSurface,
                    ),
                  ];
                  return stacked
                      ? Column(
                          children: [
                            for (final metric in totals)
                              Padding(
                                padding: const EdgeInsets.only(bottom: 8),
                                child: metric,
                              ),
                          ],
                        )
                      : Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            for (final metric in totals)
                              Expanded(child: metric),
                          ],
                        );
                },
              ),
              const SizedBox(height: 12),
              Text(
                !m.available
                    ? '统计暂不可用${m.totalBytes == null ? '' : ' · 显示上次累计'}'
                    : !m.complete
                    ? '本次统计不完整 · 仅显示可核实流量'
                    : '本次连接 · 仅代理业务流量',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: colors.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
      );
    },
  );
}

class _Metric extends StatelessWidget {
  const _Metric({
    required this.label,
    required this.amount,
    required this.color,
    this.icon,
    this.speed = false,
  });
  final String label;
  final ({String value, String unit}) amount;
  final Color color;
  final IconData? icon;
  final bool speed;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      width: double.infinity,
      padding: EdgeInsets.all(speed ? 14 : 4),
      decoration: speed
          ? BoxDecoration(
              color: theme.colorScheme.surfaceContainer,
              borderRadius: BorderRadius.circular(22),
            )
          : null,
      child: TrafficMetricContent(
        data: TrafficMetricData(
          label: label,
          value: amount.value,
          unit: amount.unit,
          icon: icon,
        ),
        color: color,
        figureStyle: theme.textTheme.titleLarge?.copyWith(
          fontSize: speed ? 27 : 21,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

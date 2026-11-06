import 'package:flutter/material.dart';

/// Formatting and availability come from each statistics adapter. In
/// particular, a missing reading must stay unknown rather than become zero.
class TrafficMetricData {
  const TrafficMetricData({
    required this.label,
    required this.value,
    required this.unit,
    this.icon,
    this.detail,
  });
  final String label, value, unit;
  final IconData? icon;
  final String? detail;
}

class TrafficMetricContent extends StatelessWidget {
  const TrafficMetricContent({
    super.key,
    required this.data,
    this.color,
    this.figureStyle,
    this.inlineUnit = false,
  });
  final TrafficMetricData data;
  final Color? color;
  final TextStyle? figureStyle;
  final bool inlineUnit;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final secondary = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    final figure =
        figureStyle ??
        theme.textTheme.titleLarge?.copyWith(
          fontSize: 27,
          fontWeight: FontWeight.w700,
          color: color,
        );
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            if (data.icon != null) ...[
              Icon(
                data.icon,
                size: 18,
                color: color ?? theme.colorScheme.onSurfaceVariant,
              ),
              const SizedBox(width: 8),
            ],
            Flexible(child: Text(data.label, style: secondary)),
          ],
        ),
        const SizedBox(height: 10),
        if (inlineUnit)
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: AlignmentDirectional.centerStart,
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(text: data.value, style: figure),
                  if (data.unit.isNotEmpty)
                    TextSpan(text: ' ${data.unit}', style: secondary),
                ],
              ),
              maxLines: 1,
              softWrap: false,
            ),
          )
        else ...[
          Text(
            data.value,
            key: ValueKey('traffic-${data.label}'),
            style: figure,
          ),
          const SizedBox(height: 2),
          Text(data.unit, style: secondary),
        ],
        if (data.detail != null) ...[
          const SizedBox(height: 2),
          Text(data.detail!, style: secondary),
        ],
      ],
    );
  }
}

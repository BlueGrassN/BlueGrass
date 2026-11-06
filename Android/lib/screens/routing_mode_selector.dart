import 'package:flutter/material.dart';

import '../app_controller.dart';
import 'components.dart';

class RoutingModeSelector extends StatelessWidget {
  const RoutingModeSelector({super.key, required this.controller});
  final AppController controller;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      const SectionTitle('分流模式', Icons.alt_route),
      const SizedBox(height: 16),
      LayoutBuilder(
        builder: (context, constraints) => SegmentedButton<String>(
          key: const Key('routing-mode'),
          direction:
              constraints.maxWidth < 300 ||
                  MediaQuery.textScalerOf(context).scale(14) > 20
              ? Axis.vertical
              : Axis.horizontal,
          segments: const [
            ButtonSegment(
              value: 'bypass_cn',
              icon: Icon(Icons.alt_route_rounded),
              label: Text('国内外分流'),
            ),
            ButtonSegment(
              value: 'global',
              icon: Icon(Icons.public_rounded),
              label: Text('全局代理'),
            ),
          ],
          showSelectedIcon: false,
          selected: {controller.routingMode},
          onSelectionChanged: controller.canChangeRoutingMode
              ? (values) => controller.setRoutingMode(values.single)
              : null,
        ),
      ),
      const SizedBox(height: 12),
      Text(
        controller.routingMode == 'bypass_cn'
            ? '国内流量直连，国外流量通过所选节点。'
            : '国内、国外公网流量均通过所选节点。',
        style: Theme.of(context).textTheme.bodySmall
            ?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant),
      ),
      if (controller.routingSaving) ...[
        const SizedBox(height: 8),
        const Text('正在保存…'),
      ] else if (!controller.canChangeRoutingMode) ...[
        const SizedBox(height: 8),
        const Text('断开连接后可切换分流模式。'),
      ],
    ],
  );
}

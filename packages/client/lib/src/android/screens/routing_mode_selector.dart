import 'package:flutter/material.dart';

import '../../presentation/routing_content.dart';
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
      RoutingModeControl(
        value: controller.routingMode,
        onChanged: controller.canChangeRoutingMode
            ? controller.setRoutingMode
            : null,
      ),
      const SizedBox(height: 12),
      RoutingModeExplanation(
        value: controller.routingMode,
        saving: controller.routingSaving,
        enabled: controller.canChangeRoutingMode,
      ),
    ],
  );
}

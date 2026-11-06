import 'package:flutter/material.dart';

import '../../../ui/navigation.dart';

const androidDestinations = [
  AppDestination(
    key: Key('nav-home'),
    label: '仪表盘',
    icon: Icons.dashboard_outlined,
    selectedIcon: Icons.dashboard_rounded,
  ),
  AppDestination(
    key: Key('nav-resources'),
    label: '代理',
    icon: Icons.public_rounded,
    selectedIcon: Icons.public_rounded,
  ),
  AppDestination(
    key: Key('nav-settings'),
    label: '设置',
    icon: Icons.tune_rounded,
    selectedIcon: Icons.tune_rounded,
  ),
  AppDestination(
    key: Key('nav-diagnostics'),
    label: '诊断',
    icon: Icons.monitor_heart_outlined,
    selectedIcon: Icons.monitor_heart_rounded,
  ),
];

/// Compatibility entry; both hosts render the shared navigation primitives.
class NavigationDock extends StatelessWidget {
  const NavigationDock({
    super.key,
    required this.index,
    required this.onSelected,
  });
  final int index;
  final ValueChanged<int> onSelected;
  @override
  Widget build(BuildContext context) => AppDock(
    destinations: androidDestinations,
    selectedIndex: index,
    onSelected: onSelected,
  );
}

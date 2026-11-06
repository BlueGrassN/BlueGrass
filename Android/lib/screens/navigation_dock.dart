import 'dart:ui' show ImageFilter;

import 'package:flutter/material.dart';

import '../surface_style.dart';

class NavigationDock extends StatelessWidget {
  const NavigationDock({
    super.key,
    required this.index,
    required this.onSelected,
  });
  final int index;
  final ValueChanged<int> onSelected;

  static const _items = [
    ('home', '仪表盘', Icons.dashboard_rounded, Icons.dashboard_outlined),
    ('resources', '代理', Icons.public_rounded, Icons.public_rounded),
    ('settings', '设置', Icons.tune_rounded, Icons.tune_rounded),
    (
      'diagnostics',
      '诊断',
      Icons.monitor_heart_rounded,
      Icons.monitor_heart_outlined,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Center(
      heightFactor: 1,
      child: Container(
        constraints: const BoxConstraints(maxWidth: 420),
        margin: const EdgeInsets.symmetric(horizontal: 20),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(40),
          boxShadow: SurfaceShadows.floating(colors.brightness),
        ),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(40),
          child: BackdropFilter(
            filter: ImageFilter.blur(sigmaX: 18, sigmaY: 18),
            child: Container(
              decoration: BoxDecoration(
                color: colors.surface.withValues(alpha: .91),
                borderRadius: BorderRadius.circular(40),
                border: Border.all(
                  color: Colors.white.withValues(
                    alpha: colors.brightness == Brightness.dark ? .09 : .8,
                  ),
                  width: .7,
                ),
              ),
              padding: const EdgeInsets.all(5),
              child: Row(
                children: [
                  for (final (i, item) in _items.indexed)
                    Expanded(
                      child: Semantics(
                        selected: i == index,
                        button: true,
                        child: Material(
                          animationDuration: const Duration(milliseconds: 180),
                          color: i == index
                              ? colors.primaryContainer
                              : Colors.transparent,
                          borderRadius: BorderRadius.circular(32),
                          child: InkWell(
                            key: Key('nav-${item.$1}'),
                            borderRadius: BorderRadius.circular(32),
                            onTap: () => onSelected(i),
                            child: Padding(
                              padding: const EdgeInsets.symmetric(
                                vertical: 9,
                                horizontal: 2,
                              ),
                              child: Column(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  Icon(
                                    i == index ? item.$3 : item.$4,
                                    size: 25,
                                    color: i == index
                                        ? colors.primary
                                        : colors.onSurfaceVariant,
                                  ),
                                  const SizedBox(height: 3),
                                  Text(
                                    item.$2,
                                    textAlign: TextAlign.center,
                                    style: TextStyle(
                                      fontSize: 11,
                                      fontWeight: FontWeight.w600,
                                      color: i == index
                                          ? colors.primary
                                          : colors.onSurfaceVariant,
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

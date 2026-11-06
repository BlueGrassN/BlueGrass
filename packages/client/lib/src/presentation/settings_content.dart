import 'package:flutter/material.dart';

class ThemeModeControl extends StatelessWidget {
  const ThemeModeControl({
    super.key,
    required this.value,
    required this.onChanged,
    this.expanded = false,
  });
  final ThemeMode value;
  final ValueChanged<ThemeMode> onChanged;
  final bool expanded;
  static const modes = [
    (ThemeMode.system, '跟随系统', Icons.brightness_auto_outlined),
    (ThemeMode.light, '浅色', Icons.light_mode_outlined),
    (ThemeMode.dark, '深色', Icons.dark_mode_outlined),
  ];
  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, box) {
      if (box.maxWidth / MediaQuery.textScalerOf(context).scale(1) < 430) {
        return Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final (mode, name, icon) in modes)
              Semantics(
                selected: mode == value,
                child: OutlinedButton.icon(
                  key: Key('theme-${mode.name}'),
                  style: OutlinedButton.styleFrom(
                    minimumSize: const Size(48, 48),
                    backgroundColor: mode == value
                        ? Theme.of(context).colorScheme.primary
                        : null,
                    foregroundColor: mode == value
                        ? Theme.of(context).colorScheme.onPrimary
                        : null,
                  ),
                  onPressed: () => onChanged(mode),
                  icon: Icon(icon),
                  label: Text(name),
                ),
              ),
          ],
        );
      }
      return SegmentedButton<ThemeMode>(
        showSelectedIcon: false,
        expandedInsets: expanded ? EdgeInsets.zero : null,
        segments: [
          for (final (mode, name, icon) in modes)
            ButtonSegment(
              value: mode,
              label: Text(name, key: Key('theme-${mode.name}')),
              icon: Icon(icon),
            ),
        ],
        selected: {value},
        onSelectionChanged: (v) => onChanged(v.first),
      );
    },
  );
}

class DiagnosticsActions extends StatelessWidget {
  const DiagnosticsActions({
    super.key,
    required this.description,
    required this.checking,
    required this.onRun,
    required this.onCopy,
  });
  final String description;
  final bool checking;
  final VoidCallback? onRun, onCopy;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Text('连接检查', style: Theme.of(context).textTheme.titleMedium),
      const SizedBox(height: 12),
      Text(
        description,
        style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
      ),
      const SizedBox(height: 16),
      Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          FilledButton.icon(
            key: const Key('run-diagnostics'),
            onPressed: onRun,
            icon: const Icon(Icons.play_arrow_rounded),
            label: Text(checking ? '正在检查' : '开始诊断'),
          ),
          OutlinedButton.icon(
            key: const Key('copy-diagnostics'),
            onPressed: onCopy,
            icon: const Icon(Icons.copy_outlined),
            label: const Text('复制诊断信息'),
          ),
        ],
      ),
      const SizedBox(height: 12),
      Text('诊断信息不包含密码、令牌或节点凭据。', style: Theme.of(context).textTheme.bodySmall),
    ],
  );
}

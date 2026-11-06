part of '../main.dart';

extension _DiagnosticsPage on _ShellState {
  Widget _diagnostics() {
    final colors = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      spacing: 24,
      children: [
        BgCard(
          padding: const EdgeInsets.fromLTRB(20, 20, 20, 20),
          child: DiagnosticsActions(
            description: '检查资源资格与本地状态。',
            checking: c.busy,
            onRun: c.busy ? null : c.diagnose,
            onCopy: c.diagnostics.isEmpty
                ? null
                : () async {
                    await Clipboard.setData(
                      ClipboardData(text: c.diagnosticJson()),
                    );
                    if (mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('已复制脱敏诊断信息')),
                      );
                    }
                  },
          ),
        ),
        if (c.diagnostics.isEmpty)
          const SettingsEmptyState(
            icon: Icons.monitor_heart_outlined,
            title: '暂无诊断结果',
            message: '开始诊断后，这里会逐项列出检查结果。',
          )
        else
          SettingsSection(
            title: '检查结果',
            children: [
              for (final item in c.diagnostics)
                ListTile(
                  leading: Icon(
                    item.passed == null
                        ? Icons.remove_circle_outline_rounded
                        : item.passed!
                        ? Icons.check_circle_outline_rounded
                        : Icons.error_outline_rounded,
                    color: item.passed == null
                        ? colors.onSurfaceVariant
                        : item.passed!
                        ? colors.success
                        : colors.warning,
                  ),
                  title: Text(item.name),
                  subtitle: Text(item.detail),
                  trailing: StatusChip(
                    item.passed == null
                        ? '未检测'
                        : item.passed!
                        ? '通过'
                        : '需处理',
                    tone: item.passed == null
                        ? StatusTone.neutral
                        : item.passed!
                        ? StatusTone.success
                        : StatusTone.warning,
                  ),
                ),
            ],
          ),
      ],
    );
  }
}

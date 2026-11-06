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
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const GlyphWell(icon: Icons.monitor_heart_outlined, size: 44),
                  const SizedBox(width: 14),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      spacing: 2,
                      children: [
                        Text('连接检查', style: AppTextStyle.headline.of(context)),
                        Text(
                          '检查资源资格与本地状态。诊断内容不包含密码、令牌或节点凭据。',
                          style: AppTextStyle.footnote.of(
                            context,
                            color: colors.onSurfaceVariant,
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 18),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  FilledButton.icon(
                    onPressed: c.busy ? null : c.diagnose,
                    icon: const Icon(Icons.play_arrow_rounded),
                    label: Text(c.busy ? '正在检查' : '开始诊断'),
                  ),
                  OutlinedButton.icon(
                    onPressed: c.diagnostics.isEmpty
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
                    icon: const Icon(Icons.copy_rounded),
                    label: const Text('复制诊断信息'),
                  ),
                ],
              ),
            ],
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

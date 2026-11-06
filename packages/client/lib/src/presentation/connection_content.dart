import 'package:flutter/material.dart';

import '../../ui/kit.dart';

/// Values are already bound to the active session by a platform adapter. A
/// selected/pending resource must not lend its identity to a recovered session.
class ConnectionViewData {
  const ConnectionViewData({this.exitIp, this.localProxy, this.detail});
  final String? exitIp, localProxy, detail;
}

class ConnectionContent extends StatelessWidget {
  const ConnectionContent({
    super.key,
    required this.data,
    this.large = false,
    this.spacing = 24,
  });
  final ConnectionViewData data;
  final bool large;
  final double spacing;
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    Widget field(String label, String value) => Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
        SizedBox(height: large ? 6 : 2),
        SelectableText(
          value,
          maxLines: large ? null : 1,
          style: large
              ? theme.textTheme.headlineSmall?.copyWith(
                  fontSize: 25,
                  letterSpacing: -.6,
                )
              : theme.textTheme.bodyMedium?.copyWith(
                  fontWeight: FontWeight.w600,
                ),
        ),
      ],
    );
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Wrap(
          spacing: spacing,
          runSpacing: 10,
          children: [
            field('节点出口 IP', data.exitIp ?? '暂未获取'),
            if (data.localProxy != null) field('本地 HTTP 代理', data.localProxy!),
          ],
        ),
        if (data.detail != null) ...[
          SizedBox(height: large ? 12 : 6),
          Text(
            data.detail!,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ],
    );
  }
}

enum ConnectionTone { idle, ok, warn }

/// FlClash's dashboard chip: 24 high, a pill, an optional status dot.
class ConnectionStatusChip extends StatelessWidget {
  const ConnectionStatusChip(
    this.label, {
    super.key,
    this.tone = ConnectionTone.idle,
    this.dot = false,
    this.textKey,
  });

  final String label;
  final ConnectionTone tone;
  final bool dot;
  final Key? textKey;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final (background, foreground) = switch (tone) {
      ConnectionTone.idle => (
        SurfaceLevel.block.colorOf(s),
        s.onSurfaceVariant,
      ),
      ConnectionTone.ok => (s.successContainer, s.onSuccessContainer),
      ConnectionTone.warn => (s.warningContainer, s.onWarningContainer),
    };
    return Container(
      constraints: const BoxConstraints(minHeight: 24),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 2),
      decoration: ShapeDecoration(color: background, shape: AppShape.pill),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (dot) ...[
            Container(
              width: 6,
              height: 6,
              decoration: BoxDecoration(
                color: foreground,
                shape: BoxShape.circle,
              ),
            ),
            const SizedBox(width: 6),
          ],
          Flexible(
            child: Text(
              label,
              key: textKey,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyle.caption1.of(
                context,
                emphasized: true,
                color: foreground,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

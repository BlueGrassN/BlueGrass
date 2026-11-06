import 'package:flutter/material.dart';

import 'expiry_reminder_coordinator.dart';
import 'resource_presentation.dart';
import 'ui/kit.dart';

class ExpiryReminderView extends StatelessWidget {
  const ExpiryReminderView({super.key, required this.coordinator});
  final ExpiryReminderCoordinator coordinator;

  @override
  Widget build(BuildContext context) {
    final items = coordinator.items;
    if (items.isEmpty) {
      return const SizedBox.shrink();
    }
    final s = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: BgCard(
        padding: EdgeInsets.zero,
        child: ListTile(
          contentPadding: const EdgeInsetsDirectional.fromSTEB(16, 4, 12, 4),
          horizontalTitleGap: 14,
          shape: AppShape.all(AppCorner.xl),
          leading: const GlyphWell(icon: Icons.event_note_outlined),
          title: Text(
            '${items.length} 项资源临近到期或已到期',
            style: AppTextStyle.subheadline.of(
              context,
              emphasized: true,
              color: s.onSurface,
            ),
          ),
          trailing: Row(
            mainAxisSize: MainAxisSize.min,
            spacing: 6,
            children: [
              const StatusChip('到期提醒', tone: StatusTone.warning),
              Icon(Icons.chevron_right_rounded, color: s.onSurfaceVariant),
            ],
          ),
          onTap: () => showExpiryList(context, coordinator),
        ),
      ),
    );
  }
}

Future<void> showExpiryList(
  BuildContext context,
  ExpiryReminderCoordinator coordinator, {
  String? notificationId,
}) => showDialog<void>(
  context: context,
  builder: (context) => ListenableBuilder(
    listenable: coordinator,
    builder: (context, _) {
      final s = Theme.of(context).colorScheme;
      final items = notificationId == null
          ? coordinator.items
          : coordinator.itemsForNotification(notificationId);
      return ResAlert(
        title: '全部资源 · 到期清单',
        content: items.isEmpty
            ? const Text('资源状态已更新或当前账号不可查看。请刷新资源后重试。')
            : SingleChildScrollView(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    if (!coordinator.coverageComplete)
                      Padding(
                        padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
                        child: Text(
                          '部分同步：以下仅含已知资源。',
                          style: AppTextStyle.footnote.of(
                            context,
                            color: s.onSurfaceVariant,
                          ),
                        ),
                      ),
                    BgCard(
                      radius: AppCorner.lg,
                      padding: EdgeInsets.zero,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          for (final (index, item) in items.indexed) ...[
                            if (index > 0)
                              Divider(
                                height: 1,
                                thickness: 1,
                                indent: 16,
                                color: s.hairline,
                              ),
                            ListTile(
                              shape: const RoundedRectangleBorder(),
                              title: Text(
                                item.name,
                                style: AppTextStyle.subheadline.of(
                                  context,
                                  emphasized: true,
                                  color: s.onSurface,
                                ),
                              ),
                              subtitle: Text(
                                formatExpiry(item.expiresAtUtc),
                                style: AppTextStyle.footnote.of(
                                  context,
                                  color: s.onSurfaceVariant,
                                ),
                              ),
                              trailing: Icon(
                                Icons.chevron_right_rounded,
                                color: s.onSurfaceVariant,
                              ),
                              onTap: () => showDialog<void>(
                                context: context,
                                builder: (context) => ListenableBuilder(
                                  listenable: coordinator,
                                  builder: (context, _) {
                                    final current = coordinator.items
                                        .where(
                                          (v) => v.key == item.key && v.owned,
                                        )
                                        .firstOrNull;
                                    return ResAlert(
                                      title: current?.name ?? '资源状态已更新',
                                      width: 360,
                                      content: Text(
                                        current == null
                                            ? '当前账号不可查看此资源，请刷新后重试。'
                                            : '${formatExpiry(current.expiresAtUtc)}'
                                                  '${current.customerRemark.isEmpty ? '' : '\n客户备注：${current.customerRemark}'}'
                                                  '\n\n本页显示只读到期摘要，不代表连接资格。购买与续费请主动前往官网。',
                                      ),
                                      actions: [
                                        ResAlertButton(
                                          label: '关闭',
                                          isDefault: true,
                                          onPressed: () =>
                                              Navigator.pop(context),
                                        ),
                                      ],
                                    );
                                  },
                                ),
                              ),
                            ),
                          ],
                        ],
                      ),
                    ),
                  ],
                ),
              ),
        actions: [
          ResAlertButton(
            label: '关闭',
            isDefault: true,
            onPressed: () => Navigator.pop(context),
          ),
        ],
      );
    },
  ),
);

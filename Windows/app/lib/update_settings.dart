import 'package:flutter/material.dart';

import 'ui/kit.dart';
import 'ui/settings_rows.dart';
import 'update_presentation.dart';

Future<void> _confirmInstall(
  BuildContext context,
  UpdatePresentation updates,
) async {
  final candidate = updates.snapshot?.candidateId;
  final confirmed = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text('安装 ${updates.snapshot?.version ?? ''}？'),
      content: const Text(
        '安装前暂停当前转发，保留 TUN 与网络保护；不会主动断开并恢复直连。'
        '确认暂停后启动 Windows 安装器，可能需要管理员授权。'
        '取消或安装失败后不会自动恢复转发，可手动重新验证连接。',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(context, true),
          child: const Text('暂停并安装'),
        ),
      ],
    ),
  );
  if (context.mounted &&
      confirmed == true &&
      candidate == updates.snapshot?.candidateId) {
    await updates.install(confirmed: true);
  }
}

/// A slim strip in the window chrome's fill above the shell, like the demo
/// notice under it: a glyph, one line and two compact actions.
class UpdateReadyBanner extends StatelessWidget {
  const UpdateReadyBanner({super.key, required this.updates});
  final UpdatePresentation updates;
  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: updates,
    builder: (context, _) {
      if (!updates.readyNoticeVisible) return const SizedBox.shrink();
      final s = Theme.of(context).colorScheme;
      const compact = Size(0, 32);
      const padding = EdgeInsets.symmetric(horizontal: 14);
      return Material(
        color: s.chrome,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 6, 12, 6),
          child: Row(
            spacing: 8,
            children: [
              Icon(Icons.system_update_alt_rounded, size: 18, color: s.primary),
              Expanded(
                child: Text(
                  '版本 ${updates.snapshot!.version} 已准备好，可在方便时安装。',
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyle.footnote.of(context, color: s.onSurface),
                ),
              ),
              TextButton(
                style: TextButton.styleFrom(
                  minimumSize: compact,
                  padding: padding,
                  foregroundColor: s.onSurfaceVariant,
                ),
                onPressed: updates.dismissReadyNotice,
                child: const Text('稍后提醒'),
              ),
              FilledButton(
                style: FilledButton.styleFrom(
                  minimumSize: compact,
                  padding: padding,
                ),
                onPressed: updates.canInstall
                    ? () => _confirmInstall(context, updates)
                    : null,
                child: const Text('安装更新'),
              ),
            ],
          ),
        ),
      );
    },
  );
}

/// The update group: one row with the state, its version and the actions,
/// then the release notes as an expandable row.
class UpdateSettings extends StatelessWidget {
  const UpdateSettings({super.key, required this.updates});
  final UpdatePresentation updates;

  Future<void> _metered(BuildContext context) async {
    final candidate = updates.snapshot?.candidateId;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('允许本次计费网络下载？'),
        content: Text(
          '仅授权当前版本 ${updates.snapshot?.version} 的这一次下载。'
          '尚需约 ${_size(updates.snapshot?.remaining ?? 0)}；不会为后续版本保留此授权。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('继续下载'),
          ),
        ],
      ),
    );
    if (context.mounted &&
        confirmed == true &&
        candidate == updates.snapshot?.candidateId) {
      await updates.download(allowMetered: true);
    }
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: updates,
    builder: (context, _) {
      final snapshot = updates.snapshot;
      final s = Theme.of(context).colorScheme;
      final secondary = AppTextStyle.footnote.of(
        context,
        color: s.onSurfaceVariant,
      );
      final actions = <Widget>[
        OutlinedButton(
          onPressed: updates.canCheck ? updates.check : null,
          child: const Text('检查更新'),
        ),
        if (updates.canDownload && !updates.waitingForNetworkConsent)
          FilledButton(onPressed: updates.download, child: const Text('下载更新')),
        if (updates.canDownload && updates.waitingForNetworkConsent)
          FilledButton(
            onPressed: () => _metered(context),
            child: const Text('允许本次下载'),
          ),
        if (updates.canDownload && !updates.userPaused)
          OutlinedButton(
            onPressed: updates.cancelDownload,
            child: const Text('暂停此版本'),
          ),
        if (updates.downloading)
          OutlinedButton(
            onPressed: updates.cancelDownload,
            child: const Text('取消下载'),
          ),
        if (updates.canInstall)
          FilledButton(
            onPressed: () => _confirmInstall(context, updates),
            child: const Text('立即安装'),
          ),
      ];
      return SettingsSection(
        title: '软件更新',
        footer: '启动后定期检查更新，符合条件时后台下载。计费或未知网络等待本次授权；安装始终由你确认。',
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Padding(
                      padding: const EdgeInsets.only(top: 2),
                      child: Icon(
                        updates.canInstall
                            ? Icons.system_update_alt_rounded
                            : Icons.update_rounded,
                        size: 24,
                        color: updates.canInstall
                            ? s.primary
                            : s.onSurfaceVariant,
                      ),
                    ),
                    const SizedBox(width: 16),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        spacing: 3,
                        children: [
                          Text(
                            updates.statusText,
                            style: AppTextStyle.subheadline
                                .of(context, color: s.onSurface)
                                .copyWith(fontWeight: FontWeight.w500),
                          ),
                          if (snapshot?.version != null)
                            Text(
                              '版本 ${snapshot!.version} · ${_size(snapshot.size)}',
                              style: secondary,
                            ),
                          if (snapshot?.version != null &&
                              snapshot!.unsupported)
                            Text(
                              '当前版本不再受支持；最低版本 ${snapshot.minimumVersion}，请尽快更新。',
                              style: AppTextStyle.footnote.of(
                                context,
                                emphasized: true,
                                color: s.warning,
                              ),
                            ),
                          if (updates.waitingForNetworkConsent && !updates.busy)
                            Text('等待非计费网络：会有限重试；也可暂停此版本。', style: secondary),
                        ],
                      ),
                    ),
                  ],
                ),
                if (updates.downloading) ...[
                  const SizedBox(height: 14),
                  Padding(
                    padding: const EdgeInsets.only(
                      left: settingsTitleInset - 16,
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      spacing: 6,
                      children: [
                        LinearProgressIndicator(
                          minHeight: 6,
                          borderRadius: BorderRadius.circular(3),
                          backgroundColor: s.tonalFill,
                          value: snapshot != null && snapshot.size > 0
                              ? snapshot.downloaded / snapshot.size
                              : null,
                        ),
                        Text(
                          '已下载 ${_size(snapshot?.downloaded ?? 0)} · 剩余 ${_size(snapshot?.remaining ?? 0)}',
                          style: AppTextStyle.caption1.of(
                            context,
                            color: s.onSurfaceVariant,
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
                const SizedBox(height: 14),
                Padding(
                  padding: const EdgeInsets.only(left: settingsTitleInset - 16),
                  child: Wrap(spacing: 8, runSpacing: 8, children: actions),
                ),
              ],
            ),
          ),
          if (snapshot?.version != null && snapshot!.notes.isNotEmpty)
            ExpansionTile(
              leading: const Icon(Icons.notes_rounded),
              title: const Text('版本说明'),
              shape: const Border(),
              collapsedShape: const Border(),
              tilePadding: const EdgeInsets.symmetric(horizontal: 16),
              childrenPadding: const EdgeInsets.fromLTRB(
                settingsTitleInset,
                0,
                16,
                16,
              ),
              expandedCrossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  snapshot.notes,
                  style: AppTextStyle.footnote
                      .of(context, color: s.onSurfaceVariant)
                      .copyWith(height: 1.5),
                ),
              ],
            ),
        ],
      );
    },
  );
}

String _size(int bytes) => bytes < 1024 * 1024
    ? '${(bytes / 1024).toStringAsFixed(1)} KiB'
    : '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MiB';

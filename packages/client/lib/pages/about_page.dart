part of '../main.dart';

extension _AboutPage on _ShellState {
  Widget _about() {
    final colors = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      spacing: 24,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(24, 8, 24, 0),
          child: Column(
            children: [
              DecoratedBox(
                decoration: ShapeDecoration(
                  color: context.raisedSurfaceColor,
                  shape: AppShape.all(AppCorner.xxl),
                ),
                child: const Padding(
                  padding: EdgeInsets.all(14),
                  child: BlueGrassMark(size: 68),
                ),
              ),
              const SizedBox(height: 18),
              const _Brand(size: 26),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                alignment: WrapAlignment.center,
                children: [
                  _aboutPill(
                    '${DesktopPlatform.current.name} 客户端 $clientVersionLabel',
                    colors.primary,
                    colors.onPrimary,
                  ),
                  _aboutPill(
                    DesktopPlatform.current.minimumSystem,
                    context.raisedSurfaceColor,
                    colors.onSurfaceVariant,
                  ),
                ],
              ),
            ],
          ),
        ),
        SettingsSection(
          title: '更多',
          dividerIndent: 72,
          children: [
            ListTile(
              leading: const GlyphWell(icon: Icons.code_rounded),
              title: const Text('开源许可'),
              trailing: const Icon(Icons.chevron_right_rounded),
              onTap: () => showLicensePage(
                context: context,
                applicationName: 'BlueGrass',
                applicationVersion: clientVersionLabel,
              ),
            ),
            ListTile(
              leading: const GlyphWell(icon: Icons.language_rounded),
              title: const Text('官方网站'),
              trailing: const Icon(Icons.open_in_new_rounded, size: 20),
              onTap: _openWebsite,
            ),
          ],
        ),
        if (widget.updates == null)
          SettingsSection(
            title: '软件更新',
            children: [
              ListTile(
                leading: const Icon(Icons.update_disabled_rounded),
                title: Text(DesktopPlatform.current.manualUpdate),
              ),
            ],
          )
        else
          UpdateSettings(updates: widget.updates!),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Text(
            '账户会话使用 ${DesktopPlatform.current.storage}；登录密码不写入磁盘。诊断由你手动导出，默认不上传。',
            style: AppTextStyle.footnote
                .of(context, color: colors.onSurfaceVariant)
                .copyWith(height: 1.6),
          ),
        ),
      ],
    );
  }

  Widget _aboutPill(String label, Color fill, Color foreground) => DecoratedBox(
    decoration: ShapeDecoration(color: fill, shape: AppShape.pill),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Text(
        label,
        style: AppTextStyle.caption1.of(
          context,
          emphasized: true,
          color: foreground,
        ),
      ),
    ),
  );
}

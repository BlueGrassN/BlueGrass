import 'package:flutter/material.dart';

import '../app_controller.dart';
import '../brand.dart';
import 'components.dart';
import 'routing_mode_selector.dart';
import 'routing_rules_card.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({
    super.key,
    required this.controller,
    this.active = true,
  });
  final AppController controller;
  final bool active;

  @override
  Widget build(BuildContext context) => ListView(
    key: const PageStorageKey('settings-scroll'),
    padding: EdgeInsets.fromLTRB(
      16,
      8,
      16,
      24 + MediaQuery.paddingOf(context).bottom,
    ),
    children: [
      SurfaceCard(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SectionTitle('外观', Icons.palette_outlined),
              const SizedBox(height: 16),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final (mode, name, icon) in [
                    (ThemeMode.system, '跟随系统', Icons.brightness_auto_outlined),
                    (ThemeMode.light, '浅色', Icons.light_mode_outlined),
                    (ThemeMode.dark, '深色', Icons.dark_mode_outlined),
                  ])
                    SelectionButton(
                      key: Key('theme-${mode.name}'),
                      label: name,
                      icon: icon,
                      selected: controller.themeMode == mode,
                      onPressed: () => controller.setTheme(mode),
                    ),
                ],
              ),
            ],
          ),
        ),
      ),
      if (controller.settingsError != null) ...[
        const SizedBox(height: 12),
        NoticeCard(controller.settingsError!, error: true),
      ],
      const SizedBox(height: 12),
      SurfaceCard(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              RoutingModeSelector(controller: controller),
              const SizedBox(height: 16),
              const _SettingsInfoRow(
                icon: Icons.vpn_lock_outlined,
                title: 'Android VPN',
                subtitle: '首次连接需授权。连接状态与实际 IPv4 出口可在“诊断”查看。',
              ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      SurfaceCard(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SectionTitle('账号', Icons.person_outline),
              const SizedBox(height: 16),
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.surfaceContainer,
                  borderRadius: BorderRadius.circular(20),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      controller.customer!.displayName.isEmpty
                          ? controller.customer!.username
                          : controller.customer!.displayName,
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                    if (controller.customer!.displayName.isNotEmpty) ...[
                      const SizedBox(height: 4),
                      Text(
                        controller.customer!.username,
                        style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                    const SizedBox(height: 8),
                    const Text('退出账号前会先停止 VPN。'),
                  ],
                ),
              ),
              if (controller.authError != null) ...[
                const SizedBox(height: 12),
                NoticeCard(controller.authError!, error: true),
              ],
              const SizedBox(height: 12),
              OutlinedButton.icon(
                key: const Key('logout'),
                onPressed: controller.authBusy ? null : controller.logout,
                icon: const Icon(Icons.logout),
                label: const Text('退出账号'),
              ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      RoutingRulesCard(
        model: controller.routingRules,
        active: active,
        vpnActive: controller.canDisconnect,
      ),
      const SizedBox(height: 12),
      SurfaceCard(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const BlueGrassBrand(size: 25),
              const SizedBox(height: 8),
              Text(
                'Android 客户端 · 0.5.0 Alpha',
                key: const Key('app-version'),
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 16),
              const Text('购买、续费与业务变更请前往官网，返回后刷新资源。'),
              const SizedBox(height: 16),
              _SettingsInfoRow(
                icon: Icons.code,
                title: '开源许可',
                onTap: () => showLicensePage(
                  context: context,
                  applicationName: 'BlueGrass',
                  applicationVersion: '0.5.0 Alpha',
                ),
              ),
              const SizedBox(height: 8),
              _SettingsInfoRow(
                icon: Icons.open_in_new,
                title: '官方网站',
                onTap: controller.openOfficialWebsite,
              ),
              if (controller.websiteError != null) ...[
                const SizedBox(height: 12),
                NoticeCard(controller.websiteError!, error: true),
              ],
            ],
          ),
        ),
      ),
    ],
  );
}

class _SettingsInfoRow extends StatelessWidget {
  const _SettingsInfoRow({
    required this.icon,
    required this.title,
    this.subtitle,
    this.onTap,
  });

  final IconData icon;
  final String title;
  final String? subtitle;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    return Material(
      color: colors.surfaceContainer,
      borderRadius: BorderRadius.circular(20),
      clipBehavior: Clip.antiAlias,
      child: ListTile(
        onTap: onTap,
        contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        leading: Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: colors.primaryContainer,
            borderRadius: BorderRadius.circular(14),
          ),
          child: Icon(icon, size: 20, color: colors.onPrimaryContainer),
        ),
        title: Text(title, style: theme.textTheme.titleSmall),
        subtitle: subtitle == null
            ? null
            : Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text(
                  subtitle!,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: colors.onSurfaceVariant,
                    height: 1.5,
                  ),
                ),
              ),
        trailing: onTap == null
            ? null
            : Icon(
                Icons.chevron_right,
                size: 20,
                color: colors.onSurfaceVariant,
              ),
      ),
    );
  }
}

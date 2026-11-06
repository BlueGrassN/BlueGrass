part of '../main.dart';

extension _SettingsPage on _ShellState {
  Widget _settings() {
    final preferences = c.preferences;
    final tun = preferences.mode == 'tun';
    final modeLocked = c.active || c.busy;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      spacing: 24,
      children: [
        SettingsSection(
          title: '外观',
          children: [
            SettingsControlRow(
              icon: Icons.palette_outlined,
              title: '主题模式',
              inlineWidth: 600,
              control: (fill) => ThemeModeControl(
                value: preferences.theme,
                expanded: fill,
                onChanged: (v) =>
                    c.updatePreferences(c.preferences.copyWith(theme: v)),
              ),
            ),
          ],
        ),
        SettingsSection(
          title: '网络连接',
          footer: DesktopPlatform.current.isMac
              ? 'TUN 连接时由系统请求管理员授权；密码不会保存在客户端。关闭后使用系统代理模式。'
              : 'TUN 模式：关闭时使用系统代理模式。',
          children: [
            SettingsControlRow(
              icon: Icons.alt_route_rounded,
              title: '路由模式',
              subtitle: !modeLocked
                  ? null
                  : c.active
                  ? '请断开连接后切换路由或 TUN 模式。'
                  : '当前操作尚未完成，请稍候再切换模式。',
              control: (fill) => _settingsRoutingSegments(modeLocked, fill),
            ),
            SwitchListTile(
              secondary: Icon(
                tun ? Icons.shield_rounded : Icons.shield_outlined,
              ),
              title: const Text('TUN 模式'),
              subtitle: Text(_takeoverModeSummary(c)),
              value: tun,
              onChanged: modeLocked || !DesktopPlatform.current.supportsTun
                  ? null
                  : (v) => c.updatePreferences(
                      c.preferences.copyWith(mode: v ? 'tun' : 'system_proxy'),
                    ),
            ),
          ],
        ),
        SettingsSection(
          title: '局域网与 DNS',
          footer: tun
              ? '允许局域网访问：允许按本地路由访问局域网；关闭普通局域网直连例外时保留必要回环和网络控制。\n'
                    '入口 DNS：仅解析中转入口；可自定义首选和备用 DNS，按列表顺序尝试。'
              : '允许局域网访问：为局域网设置系统代理绕过规则；仅影响遵循系统代理的应用，不提供全局过滤。\n'
                    '入口 DNS：仅解析中转入口；可自定义首选和备用 DNS，按列表顺序尝试。',
          children: [
            SwitchListTile(
              secondary: const Icon(Icons.lan_outlined),
              title: const Text('允许局域网访问'),
              subtitle: Text(preferences.allowLan ? '已允许' : '未允许'),
              value: preferences.allowLan,
              onChanged: c.active
                  ? null
                  : (v) => c.updatePreferences(
                      c.preferences.copyWith(allowLan: v),
                    ),
            ),
            EntryDnsSettings(
              value: preferences.entryDns,
              onSave: (policy) async {
                final value = c.preferences.copyWith(entryDns: policy);
                await c.updatePreferences(value);
                return identical(c.preferences, value);
              },
            ),
          ],
        ),
        SettingsSection(
          title: '启动与窗口',
          footer:
              '${DesktopPlatform.current.startupFooter}\n'
              '启动后自动连接：仅连接当前账号与数据源上次成功的资源；无历史时等待手动连接。',
          children: [
            SwitchListTile(
              secondary: const Icon(Icons.power_settings_new_rounded),
              title: Text(DesktopPlatform.current.startupTitle),
              subtitle: Text(
                c.demo
                    ? '开发预览，不登记 ${DesktopPlatform.current.name} 启动项'
                    : preferences.startWithWindows
                    ? '已开启'
                    : '已关闭',
              ),
              value: preferences.startWithWindows,
              onChanged: _savingStartup ? null : _setStartup,
            ),
            SwitchListTile(
              secondary: const Icon(Icons.bolt_outlined),
              title: const Text('启动后自动连接'),
              subtitle: Text(preferences.autoConnect ? '已开启' : '已关闭'),
              value: preferences.autoConnect,
              onChanged: (value) => c.updatePreferences(
                c.preferences.copyWith(autoConnect: value),
              ),
            ),
            ListTile(
              leading: const Icon(Icons.close_rounded),
              title: const Text('关闭窗口'),
              subtitle: Text(DesktopPlatform.current.closeWindow),
            ),
          ],
        ),
        if (widget.updates != null) UpdateSettings(updates: widget.updates!),
        if (widget.crashReports != null)
          CrashReportSettings(reports: widget.crashReports!),
      ],
    );
  }

  Widget _settingsRoutingSegments(bool locked, bool fill) => RoutingModeControl(
    value: c.preferences.routingMode,
    expanded: fill,
    onChanged: locked
        ? null
        : (value) =>
              c.updatePreferences(c.preferences.copyWith(routingMode: value)),
  );
}

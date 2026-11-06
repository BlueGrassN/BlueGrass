import 'dart:async';

import 'package:flutter/foundation.dart'
    show LicenseRegistry, LicenseEntryWithLineBreaks;

import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app_controller.dart';
import 'desktop_platform.dart';
import 'app_theme.dart';
import 'connection_artwork.dart';
import 'platform_store.dart';
import 'user_lifecycle.dart';
import 'resource_presentation.dart';
import 'owned_resources_view.dart';
import 'expiry_reminder_coordinator.dart';
import 'expiry_reminder_view.dart';
import 'windows_reminder_notifications.dart';
import 'successful_resource_history.dart';
import 'entry_dns_settings.dart';
import 'update_client.dart';
import 'update_presentation.dart';
import 'update_settings.dart';
import 'update_user_state.dart';
import 'crash_reports.dart';
import 'crash_report_settings.dart';
import 'crash_error_hooks.dart';
import 'app_version.dart';
import 'src/presentation/login_content.dart';
import 'src/presentation/connection_content.dart';
import 'src/presentation/traffic_content.dart';
import 'src/presentation/settings_content.dart';
import 'src/presentation/routing_content.dart';
import 'src/presentation/resource_content.dart';
import 'ui/kit.dart';
import 'ui/navigation.dart';
import 'ui/speed_chart.dart';
import 'ui/start_button.dart';
import 'ui/settings_rows.dart';
import 'ui/home_resource_picker.dart';

part 'pages/login_page.dart';
part 'pages/home_page.dart';
part 'pages/resources_page.dart';
part 'pages/settings_page.dart';
part 'pages/diagnostics_page.dart';
part 'pages/about_page.dart';

void main() {
  LicenseRegistry.addLicense(() async* {
    yield LicenseEntryWithLineBreaks([
      'Xray-core v26.10.17',
    ], await rootBundle.loadString('assets/LICENSE.Xray-core'));
    if (!DesktopPlatform.current.isMac) {
      yield LicenseEntryWithLineBreaks([
        'Wintun 0.14.1 (Windows TUN driver)',
      ], await rootBundle.loadString('assets/LICENSE.wintun'));
    }
    yield LicenseEntryWithLineBreaks([
      'sing (network utilities)',
    ], await rootBundle.loadString('assets/LICENSE.sing'));
  });
  runApp(const MyApp());
}

class MyApp extends StatefulWidget {
  const MyApp({
    super.key,
    this.controller,
    this.reminders,
    this.updates,
    this.crashReports,
  });
  final AppController? controller;
  final ExpiryReminderCoordinator? reminders;
  final UpdatePresentation? updates;
  final CrashReportCoordinator? crashReports;
  @override
  State<MyApp> createState() => _MyAppState();
}

class _MyAppState extends State<MyApp> with WidgetsBindingObserver {
  final shellKey = GlobalKey<_ShellState>();
  late final bool _ownsReminders =
      widget.controller == null && widget.reminders == null;
  late final ExpiryReminderCoordinator? reminders =
      widget.reminders ??
      widget.controller?.reminders ??
      (widget.controller == null
          ? ExpiryReminderCoordinator(
              store: WindowsReminderStore(),
              notifications: WindowsReminderNotifications(),
            )
          : null);
  late final AppController controller =
      widget.controller ??
      AppController.production(
        reminders: reminders,
        history: SuccessfulResourceHistory(),
        crashReports: crashReports,
      );
  late final CrashReportCoordinator? crashReports =
      widget.crashReports ??
      (widget.controller == null ? CrashReportCoordinator() : null);
  CrashErrorHooks? _crashHooks;
  late final UpdateClient? _updateClient =
      DesktopPlatform.current.supportsWindowsUpdater &&
          widget.controller == null &&
          widget.updates == null
      ? UpdateClient()
      : null;
  late final UpdatePresentation? updates =
      !DesktopPlatform.current.supportsWindowsUpdater
      ? null
      : widget.updates ??
            (_updateClient == null
                ? null
                : UpdatePresentation(
                    client: _updateClient,
                    pauseStore: WindowsUpdatePauseStore(),
                    pauseBeforeInstall: controller.prepareForUpdate,
                    handoffAfterLaunch: controller.handoffUpdate,
                    exitAfterHandoff: () => platform.invokeMethod<void>('exit'),
                  ));
  String? _pendingActivation, _previousAccount;
  bool _drainingActivations = false,
      _drainAgain = false,
      _openingActivation = false;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    if (widget.controller == null && crashReports != null) {
      _crashHooks = CrashErrorHooks(crashReports!)..attach();
    }
    platform.setMethodCallHandler((call) async {
      if (call.method == 'exitRequested') {
        if (await controller.disconnect()) {
          await platform.invokeMethod<void>('exit');
        }
      } else if (call.method == 'toggleConnection') {
        if (controller.active) {
          await controller.disconnect();
        } else {
          await shellKey.currentState?._manualConnect();
        }
      } else if (call.method == 'expiryNotificationActivated') {
        final id = call.arguments;
        if (id is String) {
          _pendingActivation = id;
          _openPendingActivation();
        }
      } else if (call.method == 'expiryNotificationAvailable') {
        await _drainActivations();
      }
    });
    controller.addListener(_syncTray);
    _syncWindowChrome();
    updates?.addListener(_syncUpdateRestriction);
    _syncUpdateRestriction();
    reminders?.addListener(_openPendingActivation);
    unawaited(_initialize());
    // An externally supplied controller with no reminders stays platform-free.
    if (reminders?.notifications is WindowsReminderNotifications) {
      unawaited(_drainActivations());
    }
  }

  Future<void> _initialize() async {
    await crashReports?.initialize();
    if (!mounted) return;
    await updates?.prepareStartup();
    if (!mounted) return;
    _syncUpdateRestriction();
    await controller.initialize(
      beforeAutomaticConnect: updates?.requireStartupDecision,
    );
    if (mounted && _updateClient != null) unawaited(updates!.startBackground());
  }

  void _syncUpdateRestriction() {
    final unsupported = updates?.verifiedUnsupported;
    // Absence during a check or a transient failure carries no new authority.
    if (unsupported != null) controller.setUpdateUnsupported(unsupported);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (_updateClient == null) return;
    if (state == AppLifecycleState.detached) {
      updates?.stopBackground();
    } else if (state == AppLifecycleState.resumed && !controller.initializing) {
      unawaited(updates!.startBackground());
    }
  }

  Future<void> _drainActivations() async {
    if (!mounted || reminders == null) return;
    if (_drainingActivations) {
      _drainAgain = true;
      return;
    }
    _drainingActivations = true;
    try {
      do {
        _drainAgain = false;
        while (mounted) {
          final id = await platform.invokeMethod<String>(
            'takeExpiryNotificationActivation',
          );
          if (id == null) break;
          _pendingActivation = id;
        }
        // A signal can arrive while an older empty native response is still
        // in flight. Re-read after that response instead of losing the signal.
      } while (mounted && _drainAgain);
    } on PlatformException {
      // In-app reminders remain available if Windows notification APIs fail.
    } on MissingPluginException {
      // Tests and unsupported hosts have no native activation queue.
    } finally {
      _drainingActivations = false;
      if (mounted) {
        _openPendingActivation();
        if (_drainAgain) unawaited(_drainActivations());
      }
    }
  }

  void _openPendingActivation() {
    if (!mounted ||
        _pendingActivation == null ||
        _openingActivation ||
        controller.initializing ||
        controller.customer == null ||
        reminders?.scopeLoaded != true ||
        reminders?.snapshot?.scope.accountKey != controller.customer?.id) {
      return;
    }
    _openingActivation = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _openingActivation = false;
      if (!mounted ||
          _pendingActivation == null ||
          reminders?.scopeLoaded != true ||
          reminders?.snapshot?.scope.accountKey != controller.customer?.id) {
        return;
      }
      final id = _pendingActivation!;
      _pendingActivation = null;
      shellKey.currentState?._openExpiryNotification(id);
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  ({int caption, int text, bool dark})? _windowChrome;

  /// Tints the native title bar with the sidebar's chrome fill (Windows 11;
  /// earlier versions keep the system caption). Native closing, snapping and
  /// resizing stay with Windows.
  void _syncWindowChrome() {
    final brightness = switch (controller.preferences.theme) {
      ThemeMode.light => Brightness.light,
      ThemeMode.dark => Brightness.dark,
      ThemeMode.system =>
        WidgetsBinding.instance.platformDispatcher.platformBrightness,
    };
    final colors = blueGrassColorScheme(brightness);
    final next = (
      caption: colors.chrome.toARGB32(),
      text: colors.onSurface.toARGB32(),
      dark: brightness == Brightness.dark,
    );
    if (next == _windowChrome) return;
    _windowChrome = next;
    unawaited(
      platform
          .invokeMethod<void>('setWindowChrome', {
            'caption': next.caption,
            'text': next.text,
            'dark': next.dark,
          })
          .catchError((Object _) {}),
    );
  }

  @override
  void didChangePlatformBrightness() {
    super.didChangePlatformBrightness();
    _syncWindowChrome();
  }

  void _syncTray() {
    _syncWindowChrome();
    final account = controller.customer?.id;
    if (_previousAccount != null && _previousAccount != account) {
      _pendingActivation = null;
    }
    _previousAccount = account;
    _openPendingActivation();
    unawaited(
      platform
          .invokeMethod<void>('setTrayState', {
            'label': controller.connectionLabel,
          })
          .catchError((Object _) {}),
    );
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _crashHooks?.dispose();
    controller.removeListener(_syncTray);
    updates?.removeListener(_syncUpdateRestriction);
    reminders?.removeListener(_openPendingActivation);
    platform.setMethodCallHandler(null);
    if (widget.controller == null) controller.dispose();
    if (_ownsReminders) reminders?.dispose();
    if (widget.updates == null) updates?.dispose();
    if (_updateClient != null) unawaited(_updateClient.dispose());
    if (widget.crashReports == null) crashReports?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: Listenable.merge([controller, ?reminders]),
    builder: (context, _) => MaterialApp(
      title: 'BlueGrass',
      debugShowCheckedModeBanner: false,
      themeMode: controller.preferences.theme,
      theme: blueGrassTheme(Brightness.light),
      darkTheme: blueGrassTheme(Brightness.dark),
      home: _Shell(
        key: shellKey,
        controller: controller,
        reminders: reminders,
        updates: updates,
        crashReports: crashReports,
      ),
    ),
  );
}

class _Shell extends StatefulWidget {
  const _Shell({
    super.key,
    required this.controller,
    this.reminders,
    this.updates,
    this.crashReports,
  });
  final AppController controller;
  final ExpiryReminderCoordinator? reminders;
  final UpdatePresentation? updates;
  final CrashReportCoordinator? crashReports;
  @override
  State<_Shell> createState() => _ShellState();
}

class _ShellState extends State<_Shell> {
  int page = 0;
  final username = TextEditingController(), password = TextEditingController();
  final search = TextEditingController();
  bool obscure = true, allResources = false;
  int _ownedRefresh = 0;
  String country = '全部地区';
  AppController get c => widget.controller;
  bool _manualConnecting = false;
  bool _savingStartup = false;
  late final lifecycle = UserLifecycle(
    preferences: () => c.preferences,
    save: (value) async {
      await c.updatePreferences(value);
      return identical(c.preferences, value);
    },
    registration: WindowsAutostartRegistration(),
  );
  static const pages = ['首页', '我的 IP', '设置', '诊断', '关于'];
  static const destinations = [
    AppDestination(
      icon: Icons.space_dashboard_outlined,
      selectedIcon: Icons.space_dashboard_rounded,
      label: '首页',
    ),
    AppDestination(
      icon: Icons.public_outlined,
      selectedIcon: Icons.public_rounded,
      label: '我的 IP',
    ),
    AppDestination(
      icon: Icons.settings_outlined,
      selectedIcon: Icons.settings_rounded,
      label: '设置',
    ),
    AppDestination(
      icon: Icons.monitor_heart_outlined,
      selectedIcon: Icons.monitor_heart_rounded,
      label: '诊断',
    ),
    AppDestination(
      icon: Icons.info_outline_rounded,
      selectedIcon: Icons.info_rounded,
      label: '关于',
    ),
  ];
  bool _sidebarExpanded = true;
  bool _resourcePickerOpen = false;

  /// Page extensions in `pages/` rebuild the shell through this.
  void _update(VoidCallback fn) => setState(fn);

  @override
  void dispose() {
    username.dispose();
    password.dispose();
    search.dispose();
    super.dispose();
  }

  Future<void> _login() async {
    if (c.initializing || c.busy || !c.canLogin || c.logoutNeedsRetry) return;
    if (username.text.trim().isEmpty || password.text.isEmpty) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('请输入账号和密码')));
      return;
    }
    await c.login(username.text, password.text);
    if (c.customer != null) password.clear();
  }

  Future<bool> _confirm(String title, String detail) async =>
      await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(title),
          content: Text(detail),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('继续'),
            ),
          ],
        ),
      ) ??
      false;
  Future<void> _select(domain.NodeRecord node) async {
    if (c.active &&
        c.selected?.key != node.key &&
        !await _confirm('切换资源', '切换出口会中断现有连接。继续后需要重新连接。')) {
      return;
    }
    await c.select(node);
    if (mounted) setState(() => page = 0);
  }

  Future<void> _logout() async {
    if (c.active && !await _confirm('退出账号', '退出前会断开当前连接。')) return;
    await c.logout();
    if (c.customer == null) await widget.reminders?.clearAccount();
    if (mounted) {
      setState(() {
        page = 0;
        search.clear();
        allResources = false;
      });
    }
  }

  void _openExpiryNotification(String id) {
    final reminders = widget.reminders;
    if (reminders == null ||
        reminders.snapshot?.scope.accountKey != c.customer?.id) {
      return;
    }
    showExpiryList(context, reminders, notificationId: id);
  }

  Future<void> _setStartup(bool value) async {
    if (_savingStartup) return;
    setState(() => _savingStartup = true);
    if (c.demo) {
      await c.updatePreferences(
        c.preferences.copyWith(startWithWindows: value),
      );
    } else {
      await lifecycle.setStartWithWindows(value);
    }
    if (mounted) {
      setState(() => _savingStartup = false);
      if (lifecycle.error != null) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(lifecycle.error!)));
      }
    }
  }

  Future<void> _manualConnect() async {
    if (_manualConnecting || c.busy || c.active || c.selected == null) return;
    _manualConnecting = true;
    final key = c.selected!.key;
    final account = c.customer?.id;
    try {
      if (c.selected!.protocol.toLowerCase() == 'socks5' &&
          !c.preferences.socksWarningShown) {
        final value = c.preferences.copyWith(socksWarningShown: true);
        await c.updatePreferences(value);
        if (!mounted || !identical(c.preferences, value)) return;
        if (!await _confirm(
          'SOCKS5 传输说明',
          'SOCKS5 本身不加密认证信息和代理传输。HTTPS 网站内容仍受其 TLS 保护；TUN 接管不等于传输加密。是否继续连接？',
        )) {
          return;
        }
      }
      if (!mounted ||
          c.selected?.key != key ||
          c.customer?.id != account ||
          c.active) {
        return;
      }
      await c.connect();
      if (!mounted || c.selected?.key != key || c.customer?.id != account) {
        return;
      }
      if (await lifecycle.claimStartupPrompt(
            manual: true,
            connected: c.connected,
          ) &&
          mounted) {
        final enable = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: Text('${DesktopPlatform.current.startupTitle} BlueGrass？'),
            content: Text(
              '首次连接已完成。开启后登录 ${DesktopPlatform.current.name} 时会打开客户端；自动连接仍由独立开关控制。',
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('暂不开启'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('开启自启'),
              ),
            ],
          ),
        );
        if (enable == true && mounted) await _setStartup(true);
      }
    } finally {
      _manualConnecting = false;
    }
  }

  Future<void> _openWebsite() async {
    const url = String.fromEnvironment(
      'BLUEGRASS_WEBSITE_URL',
      defaultValue: 'https://sunipip.com/',
    );
    final uri = publicWebsiteUri(url);
    if (uri == null) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('WEB-001 · 官网入口未配置或无效，请使用已知的官网地址。')),
        );
      }
      return;
    }
    try {
      if (c.active && !c.connected) {
        if (!await _confirm(
          '恢复网络并打开官网',
          '当前连接或保护状态可能限制官网访问。继续会先断开并恢复接管前网络设置；恢复成功后打开官网。',
        )) {
          return;
        }
        if (!await c.disconnect()) return;
      }
      await platform.invokeMethod<void>('openUrl', {'url': uri.toString()});
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('无法打开浏览器，请手动访问官网。')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Scaffold(
      body: Column(
        children: [
          if (widget.updates != null)
            UpdateReadyBanner(updates: widget.updates!),
          if (c.demo)
            Container(
              width: double.infinity,
              color: colors.chrome,
              padding: const EdgeInsets.symmetric(vertical: 5, horizontal: 16),
              child: Row(
                spacing: 8,
                children: [
                  const StatusChip('开发预览', tone: StatusTone.warning),
                  Expanded(
                    child: Text(
                      '全部为模拟数据，未接管系统网络',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyle.caption1.of(
                        context,
                        color: colors.onSurfaceVariant,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          if (!c.demo &&
              c.active &&
              !c.connected &&
              !c.connecting &&
              c.connection != 'stopping')
            MaterialBanner(
              leading: Icon(
                Icons.shield_outlined,
                color: colors.onWarningContainer,
              ),
              content: Text(
                c.actualMode == 'tun'
                    ? '转发已暂停或状态待确认，保留网络保护。可在验证条件满足时重新连接，或主动恢复网络并断开。'
                    : '代理尚未释放，请先恢复并断开。其他程序修改的代理设置会保留。',
              ),
              actions: [
                if (c.canRetryProtected)
                  TextButton(
                    onPressed: c.busy ? null : c.retryProtectedConnection,
                    child: const Text('重新验证并连接'),
                  ),
                TextButton(
                  onPressed: c.busy ? null : () => c.disconnect(),
                  child: Text(c.actualMode == 'tun' ? '恢复网络并断开' : '恢复并断开代理'),
                ),
              ],
            ),
          Expanded(
            child: LayoutBuilder(
              builder: (context, size) {
                if (c.customer == null) return _loginPage(size.maxWidth);
                final width = size.maxWidth;
                // Below FlClash's laptop breakpoint the destinations move to a
                // floating dock, so every label stays visible and tappable.
                final docked = usesNavigationDock(width);
                final mobile = width < 600;
                final content = Column(
                  children: [
                    PageHeader(
                      title: pages[page],
                      padding: EdgeInsets.fromLTRB(
                        mobile ? 16 : 24,
                        mobile ? 12 : 16,
                        mobile ? 12 : 20,
                        8,
                      ),
                      actions: [
                        if (page < 2)
                          HeaderAction(
                            icon: Icons.refresh_rounded,
                            tooltip: '刷新资源',
                            busy: c.loading,
                            onPressed: c.loading && !allResources
                                ? null
                                : () {
                                    if (page == 1 && allResources) {
                                      setState(() => _ownedRefresh++);
                                    } else {
                                      c.refresh();
                                    }
                                  },
                          ),
                        if (docked)
                          HeaderAction(
                            icon: Icons.logout_rounded,
                            tooltip: '退出账号',
                            onPressed: _logout,
                          ),
                      ],
                    ),
                    Expanded(child: _pageBody(mobile)),
                  ],
                );
                return ClientNavigationFrame(
                  destinations: destinations,
                  selectedIndex: page,
                  onSelected: (i) => setState(() => page = i),
                  expanded: _sidebarExpanded,
                  onToggle: () =>
                      setState(() => _sidebarExpanded = !_sidebarExpanded),
                  brand: const _Brand(size: 19),
                  footer: _sidebarFooter,
                  child: content,
                );
              },
            ),
          ),
        ],
      ),
    );
  }

  Widget _pageBody(bool mobile) => SingleChildScrollView(
    padding: EdgeInsets.fromLTRB(
      mobile ? 16 : 24,
      8,
      mobile ? 16 : 24,
      mobile ? 16 : 28,
    ),
    child: Align(
      alignment: Alignment.topCenter,
      child: ConstrainedBox(
        // The dashboard widens to four 440 dp columns; reading pages stay
        // at a comfortable measure.
        constraints: BoxConstraints(maxWidth: page == 0 ? 1808 : 1120),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (widget.reminders != null &&
                widget.reminders!.snapshot?.scope.accountKey == c.customer?.id)
              ExpiryReminderView(coordinator: widget.reminders!),
            if (c.message != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 16),
                child: _Notice(c.message!, icon: Icons.info_outline_rounded),
              ),
            switch (page) {
              0 => _home(),
              1 => _resources(),
              2 => _settings(),
              3 => _diagnostics(),
              _ => _about(),
            },
          ],
        ),
      ),
    ),
  );

  Widget _sidebarFooter(BuildContext context, bool expanded) {
    final colors = Theme.of(context).colorScheme;
    final customer = c.customer!;
    final name = customer.displayName.isEmpty
        ? customer.username
        : customer.displayName;
    if (!expanded) {
      return Padding(
        padding: const EdgeInsets.only(top: 8),
        child: Center(
          child: IconButton(
            tooltip: '退出账号',
            onPressed: c.busy ? null : _logout,
            icon: const Icon(Icons.logout_rounded),
          ),
        ),
      );
    }
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Divider(color: colors.hairline),
          const SizedBox(height: 12),
          Row(
            children: [
              const SurfaceScope(
                level: SurfaceLevel.card,
                child: GlyphWell(icon: Icons.person_outline_rounded, size: 34),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyle.subheadline.of(
                        context,
                        emphasized: true,
                      ),
                    ),
                    Text(
                      'BlueGrass 账号',
                      style: AppTextStyle.caption1.of(
                        context,
                        color: colors.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: c.busy ? null : _logout,
              icon: const Icon(Icons.logout_rounded, size: 18),
              label: const Text('退出账号'),
            ),
          ),
        ],
      ),
    );
  }
}

String _expiry(DateTime? time, {bool noResource = false}) {
  if (noResource) return '—';
  return formatExpiry(time);
}

// A preference describes the next connection; a live session may have been
// restored in a different mode, so only its reported mode describes takeover.
String _takeoverModeSummary(AppController c) {
  if (!DesktopPlatform.current.supportsTun) {
    return DesktopPlatform.tunUnavailable;
  }
  if (c.active) {
    return switch (c.actualMode) {
      'tun' => '实际接管：TUN',
      'system_proxy' => '实际接管：系统代理',
      _ => '实际接管模式待确认',
    };
  }
  return c.preferences.mode == 'tun' ? '下次连接使用 TUN' : '下次连接使用系统代理';
}

String _bytes(BigInt? value) {
  if (value == null) return '—';
  var number = value.toDouble();
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  var index = 0;
  while (number >= 1024 && index < units.length - 1) {
    number /= 1024;
    index++;
  }
  return '${number.toStringAsFixed(index == 0 ? 0 : 1)} ${units[index]}';
}

String _speed(double? value) =>
    value == null ? '—' : '${_bytes(BigInt.from(value.round()))}/s';

class _Brand extends StatelessWidget {
  const _Brand({this.size = 23});
  final double size;
  @override
  Widget build(BuildContext context) => FittedBox(
    fit: BoxFit.scaleDown,
    alignment: Alignment.centerLeft,
    child: Image.asset(
      Theme.of(context).brightness == Brightness.dark
          ? 'assets/branding/wordmark-dark.png'
          : 'assets/branding/wordmark-light.png',
      width: size * 178.5 / 28,
      height: size * 44 / 28,
      fit: BoxFit.contain,
      filterQuality: FilterQuality.high,
      semanticLabel: 'BlueGrass',
    ),
  );
}

class _Notice extends StatelessWidget {
  const _Notice(this.text, {this.icon = Icons.info_outline_rounded});
  final String text;
  final IconData icon;
  @override
  Widget build(BuildContext context) => BgNotice(text, icon: icon);
}

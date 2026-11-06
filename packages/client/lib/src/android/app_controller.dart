import 'dart:async';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart' show ChangeNotifier, ThemeMode;

import '../application/account_resources.dart';
import 'platform/android_store.dart';
import 'platform/official_website.dart' as website;
import 'platform/routing_rules_client.dart';
import 'platform/vpn_client.dart';
import 'resource_presentation.dart';
import 'routing_mode.dart';
import 'routing_rules.dart';
import 'vpn_session.dart';
import 'traffic.dart';

enum ResourceView { connectable, subscriptions, assets }

class AppController extends ChangeNotifier {
  AppController({
    required this._source,
    required this._store,
    DateTime Function()? clock,
    VpnClient? vpnClient,
    Duration vpnRefreshInterval = const Duration(minutes: 1),
    Future<void> Function()? websiteLauncher,
    RoutingRulesClient? routingRulesClient,
    TrafficClient? trafficClient,
  }) : _clock = clock ?? DateTime.now,
       routingRules = RoutingRulesModel(client: routingRulesClient),
       traffic = TrafficModel(client: trafficClient),
       _websiteLauncher = websiteLauncher ?? website.openOfficialWebsite,
       _vpn = VpnSession(
         source: _source,
         client: vpnClient,
         clock: clock ?? DateTime.now,
         refreshInterval: vpnRefreshInterval,
       ) {
    _vpn.addListener(_changed);
  }
  final VpnSession _vpn;
  final RoutingRulesModel routingRules;
  final TrafficModel traffic;
  final Future<void> Function() _websiteLauncher;
  String? websiteError;
  Future<void> openOfficialWebsite() async {
    websiteError = null;
    try {
      await _websiteLauncher();
    } catch (_) {
      if (!_disposed) websiteError = '无法打开官网，请在浏览器访问 https://sunipip.com/';
    }
    _changed();
  }

  VpnSnapshot? get vpnSnapshot => _vpn.snapshot;
  VpnState get vpnState => _vpn.state;
  bool get vpnAvailable => _vpn.available;
  bool get vpnBusy => _vpn.busy || vpnState == VpnState.connecting;
  bool get canConnect =>
      !authBusy &&
      _routingInitialized &&
      !routingSaving &&
      !requiresSessionClear &&
      selected != null &&
      _vpn.canConnect;
  bool get canDisconnect => !authBusy && (_vpn.active || _vpn.busy);
  String? get vpnError => _vpn.error;
  ResourceItem? get activeResource => _vpn.activeResource;
  VpnObservationState get observationState => _vpn.observationState;
  String? get observedExitIp => _vpn.observedExitIp;
  Future<void> connect() async {
    if (canConnect) await _vpn.connect(selected!);
  }

  Future<void> disconnect() => _vpn.disconnect();
  Future<void> observeExit() => _vpn.observeExit();
  Future<void> refreshVpnEligibility() => _vpn.refreshEligibility();
  bool routingSaving = false;
  bool _routingInitialized = false;
  String get routingMode => _vpn.active
      ? _vpn.snapshot?.routingMode ?? _vpn.routingMode
      : _vpn.routingMode;
  bool get canChangeRoutingMode =>
      initialized &&
      _routingInitialized &&
      !authBusy &&
      !requiresSessionClear &&
      !routingSaving &&
      !_vpn.busy &&
      !_vpn.active;

  Future<void> setRoutingMode(String value) async {
    if (!canChangeRoutingMode || value == routingMode) return;
    if (!routingModes.contains(value)) {
      settingsError = '不支持此分流模式。';
      _changed();
      return;
    }
    routingSaving = true;
    settingsError = null;
    _changed();
    try {
      await _store.writeRoutingMode(value);
      if (!_disposed) _vpn.routingMode = value;
    } catch (_) {
      if (!_disposed) settingsError = '分流模式保存失败，请重试。';
    } finally {
      routingSaving = false;
      _changed();
    }
  }

  Future<void> _initializeRoutingMode() async {
    if (_routingInitialized) return;
    final value = await _store.readRoutingMode();
    if (_disposed) return;
    _vpn.routingMode = value;
    _routingInitialized = true;
  }

  Future<void> resumeVpn() async {
    // Permission UI itself causes a resume. Its in-flight connect owns recovery.
    if (_disposed || authBusy || !initialized || _vpn.busy) return;
    try {
      await _vpn.recover(customer?.id);
    } catch (_) {
      /* safe VPN error is shown */
    }
  }

  final CustomerDataSource _source;
  final AppStore _store;
  final DateTime Function() _clock;
  late final _account = AccountResources<ResourceItem>(
    source: () => _source,
    keyOf: (item) => item.key,
    eligible: (item) =>
        item.eligible &&
        (item.expiresAtUtc == null || nowUtc.isBefore(item.expiresAtUtc!)),
    visible: (item) => isResourceVisible(item.expiresAtUtc, nowUtc),
  );
  int get _accountRevision => _account.generation;
  int _themeRevision = 0;
  bool _disposed = false;
  String? _deviceId;
  Future<void>? _themeWrite;
  Customer? get customer => _account.customer;
  set customer(Customer? value) => _account.customer = value;
  ResourceItem? get selected => _account.selected;
  set selected(ResourceItem? value) => _account.selected = value;
  List<ResourceItem> get resources => _account.items;
  set resources(List<ResourceItem> value) => _account.items = value;
  bool get hasMore => _account.hasMore;
  set hasMore(bool value) => _account.hasMore = value;
  bool get loading => _account.loading;
  set loading(bool value) => _account.loading = value;
  bool requiresSessionClear = false;
  bool initialized = false, authBusy = false;
  bool resourceQueries = false;
  String? authError, resourceError, settingsError, notice;
  ThemeMode themeMode = ThemeMode.system;
  ResourceView view = ResourceView.connectable;
  DateTime get nowUtc =>
      (_source is ApiV2DataSource ? _source.effectiveNowUtc : null) ??
      _clock().toUtc();
  bool _current(int revision) => _account.current(revision);
  void _changed() {
    if (!_disposed) {
      traffic.bind(_vpn.snapshot, customer?.id);
      notifyListeners();
    }
  }

  Future<void> initialize() async {
    if (authBusy) return;
    final revision = _account.beginAccountChange();
    authBusy = true;
    authError = null;
    _changed();
    try {
      final themeRevision = _themeRevision;
      final theme = await _store.readTheme();
      // Read/migrate before deviceId creates a record on a fresh installation.
      await _initializeRoutingMode();
      final id = await _store.deviceId();
      if (!_current(revision)) return;
      if (themeRevision == _themeRevision) themeMode = theme;
      _deviceId = id;
      final restored = await _source.restore();
      if (!_current(revision)) return;
      await _vpn.recover(restored?.id);
      if (!_current(revision)) return;
      customer = restored;
      if (restored != null) await _loadAccount(revision);
    } catch (error) {
      if (_current(revision)) {
        authError = friendlyError(error);
        requiresSessionClear =
            error is VpnFailure ||
            (error is ApiFailure &&
                {
                  'storage_error',
                  'invalid_session',
                  'environment_mismatch',
                }.contains(error.code));
      }
    } finally {
      if (_current(revision)) {
        authBusy = false;
        initialized = true;
        _changed();
      }
    }
  }

  Future<void> login(String username, String password) async {
    if (authBusy || requiresSessionClear) return;
    if (username.trim().isEmpty || password.isEmpty) {
      authError = '请输入账号和密码。';
      _changed();
      return;
    }
    final revision = _account.beginAccountChange();
    authBusy = true;
    authError = null;
    notice = null;
    _vpn.beginAccountChange();
    _changed();
    var sourceStarted = false;
    try {
      await _vpn.stop();
      if (!_current(revision)) return;
      _resetResources();
      // Clearing a failed initialization does not make its migration succeed.
      await _initializeRoutingMode();
      if (!_current(revision)) return;
      final id = _deviceId ?? await _store.deviceId();
      if (!_current(revision)) return;
      _deviceId = id;
      sourceStarted = true;
      final account = await _source.login(
        username: username.trim(),
        password: password,
        deviceId: id,
        deviceName: 'BlueGrass Android',
      );
      if (!_current(revision)) return;
      await _vpn.recover(account.id);
      if (!_current(revision)) return;
      customer = account;
      await _loadAccount(revision);
    } catch (error) {
      if (_current(revision)) {
        if (sourceStarted) customer = null;
        _vpn.cancelAccountChange();
        authError = error is VpnFailure
            ? 'VPN 尚未停止，无法切换账号，请重试。'
            : friendlyError(error);
      }
    } finally {
      if (_current(revision)) {
        authBusy = false;
        _changed();
      }
    }
  }

  Future<void> _loadAccount(int revision) async {
    await _reloadCapabilities(revision);
    if (_current(revision)) await _load(reset: true);
  }

  Future<void> _reloadCapabilities(int revision) async {
    if (_source is ResourceQueryDataSource) {
      try {
        final caps = await _account.refreshCapabilities();
        if (!_current(revision) || caps == null) return;
        resourceQueries = caps.resourceQueries;
        notice = null;
      } catch (error) {
        if (!_current(revision)) return;
        if (_sessionEnded(error)) {
          await _expireSession(error);
          return;
        }
        notice = '完整资源视图暂不可用，可以先查看可连接资源。';
      }
    }
  }

  void _resetResources() {
    _account.resetResources();
    resourceError = null;
    resourceQueries = false;
    view = ResourceView.connectable;
  }

  Future<void> logout() async {
    final revision = _account.beginAccountChange();
    authBusy = true;
    authError = null;
    notice = null;
    _vpn.beginAccountChange();
    _changed();
    var stopped = false;
    try {
      await _vpn.stop();
      if (!_current(revision)) return;
      stopped = true;
      customer = null;
      _resetResources();
      await _source.logout();
      if (_current(revision)) requiresSessionClear = false;
    } catch (error) {
      if (_current(revision)) {
        if (!stopped) {
          _vpn.cancelAccountChange();
          authError = 'VPN 停止失败，尚未退出登录，请重试。';
          return;
        }
        final localCleared =
            error is ApiFailure && error.code == 'logout_remote_unconfirmed';
        requiresSessionClear = !localCleared;
        authError = friendlyError(error);
      }
    } finally {
      if (_current(revision)) {
        authBusy = false;
        initialized = true;
        _changed();
      }
    }
  }

  bool _sessionEnded(Object error) =>
      (customer != null && _source.customer == null) ||
      (error is ApiFailure &&
          (error.terminal ||
              error.status == 401 ||
              error.code == 'not_authenticated'));
  Future<void> _expireSession(Object error) async {
    if (_vpn.active) {
      // API authentication expiry is not revocation of an established tunnel.
      // ADR0015 retains its last verified native lease until known expiry.
      _vpn.noteSyncFailure();
      requiresSessionClear = true;
      authError = friendlyError(error);
      _changed();
      return;
    }
    final logoutRevision = _accountRevision + 1;
    await logout();
    if (_current(logoutRevision) && !requiresSessionClear) {
      authError = friendlyError(error);
      _changed();
    }
  }

  Future<void> changeView(ResourceView value) async {
    if (value == view) return;
    view = value;
    await refresh();
  }

  Future<void> refresh() async {
    if (customer == null || _disposed) return;
    final revision = _accountRevision;
    if (_account.retryCapabilities) await _reloadCapabilities(revision);
    if (_current(revision)) {
      // Start the page with the view selected by this refresh, before any VPN
      // lookup yields to a later change of resource view.
      await Future.wait([_load(reset: true), _vpn.refreshEligibility()]);
    }
  }

  Future<void> loadMore() async {
    if (!loading && hasMore) await _load(reset: false);
  }

  Future<void> _load({required bool reset}) async {
    final activeView = view;
    resourceError = null;
    await _account.load(
      reset: reset,
      clearOnReset: true,
      clearSelectionOnReset: activeView == ResourceView.connectable,
      changed: _changed,
      fetch: (cursor) async {
        if (activeView == ResourceView.connectable) {
          final page = await _source.listNodes(cursor: cursor);
          return Page(
            items: page.items
                .map((node) => ResourceItem.fromNode(node, nowUtc))
                .toList(),
            hasMore: page.hasMore,
            nextCursor: page.nextCursor,
          );
        }
        if (!resourceQueries) throw const ApiFailure('capability_unavailable');
        final page = await _source.listResources(
          kind: activeView == ResourceView.assets ? 'asset' : 'subscription',
          cursor: cursor,
        );
        return Page(
          items: page.items.map(ResourceItem.fromOwned).toList(),
          hasMore: page.hasMore,
          nextCursor: page.nextCursor,
        );
      },
      onLoaded: () async {
        if (activeView == ResourceView.connectable) {
          _account.reconcileSelection();
        }
      },
      onError: (error, currentQuery) async {
        if (_sessionEnded(error)) {
          await _expireSession(error);
        } else if (currentQuery) {
          resourceError = friendlyError(error);
        }
      },
    );
  }

  void select(ResourceItem value) {
    if (_account.select(value)) _changed();
  }

  Future<void> setTheme(ThemeMode value) {
    // Queue persistence so rapid choices cannot be committed out of order.
    return _themeWrite = (_themeWrite ?? Future<void>.value()).then((_) async {
      if (_disposed) return;
      try {
        await _store.writeTheme(value);
        if (!_disposed) {
          themeMode = value;
          _themeRevision++;
          settingsError = null;
        }
      } catch (_) {
        if (!_disposed) settingsError = '主题保存失败，请重试。';
      }
      _changed();
    });
  }

  @override
  void dispose() {
    _disposed = true;
    _account.dispose();
    _vpn.dispose();
    routingRules.dispose();
    traffic.dispose();
    super.dispose();
  }
}

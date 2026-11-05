import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import 'platform_store.dart';
import 'preferences.dart';
import 'native_client.dart';
import 'successful_resource_history.dart';
import 'expiry_reminder_coordinator.dart';
import 'diagnostic_client.dart';
import 'crash_reports.dart';
import 'app_version.dart';
import 'control_http_transport.dart';
import 'update_client.dart';
import 'api_endpoint.dart';
import 'desktop_platform.dart';

const apiUrl = String.fromEnvironment(
  'BLUEGRASS_API_URL',
  defaultValue: 'https://client-api.sunip.vip/api/client/v1',
);

/// Owns the UI lifetime; data-source and native boundaries own their own I/O.
class AppController extends ChangeNotifier {
  AppController({
    required this.source,
    this.demo = false,
    Future<String> Function()? getDeviceId,
    Future<Preferences> Function()? readPreferences,
    Future<void> Function(Preferences)? savePreferences,
    this.nativeFactory,
    this.history,
    this.reminders,
    this.updateResumeStore,
    this.crashReports,
    this.installedBuild = clientBuild,
  }) : _getDeviceId = getDeviceId ?? deviceId,
       _readPreferences = readPreferences ?? Preferences.read,
       _savePreferences = savePreferences ?? ((value) => value.save());

  factory AppController.production({
    SuccessfulResourceHistory? history,
    ExpiryReminderCoordinator? reminders,
    CrashReportCoordinator? crashReports,
    HttpTransport? unprotectedTransport,
    HttpTransport? bootstrapTransport,
    NativeClient Function()? nativeFactory,
  }) {
    late AppController controller;
    final HttpTransport directTransport =
        unprotectedTransport ??
        IoHttpTransport(
          explicitProxy:
              const String.fromEnvironment('BLUEGRASS_API_PROXY').isEmpty
              ? null
              : Uri.parse(const String.fromEnvironment('BLUEGRASS_API_PROXY')),
        );
    // macOS has system proxy only; its host intentionally has no physically
    // bound Windows control socket. Dart HTTPS uses an explicit DIRECT path by
    // default, independent of the system proxy we own, with normal TLS checks.
    final nativeTransport = DesktopPlatform.current.isMac
        ? null
        : ControlHttpTransport(
            resolveHost: (host) => controller._resolveControlHost(host),
            protectionActive: () => controller.actualMode == 'tun',
            unprotectedTransport: directTransport,
          );
    final HttpTransport transport = nativeTransport ?? directTransport;
    controller = AppController(
      history: history,
      reminders: reminders,
      crashReports: crashReports,
      updateResumeStore: WindowsSessionStore('update_resume_v1'),
      source: ApiV2DataSource(
        baseUri: Uri.parse(apiUrl),
        store: WindowsSessionStore('session_v1'),
        transport: transport,
      ),
      nativeFactory: nativeFactory ?? NativeClient.new,
    );
    controller._disposeTransport = nativeTransport?.dispose;
    controller._configureApi = () async {
      final endpoint = await loadApiEndpoint(
        store: WindowsSessionStore('api_endpoint_v1'),
        transport: bootstrapTransport,
        // Recover an existing protected session before any ordinary DNS/HTTP.
        allowNetwork: controller.actualMode != 'tun',
      );
      await nativeTransport?.configure(endpoint);
      controller.source = ApiV2DataSource(
        baseUri: endpoint,
        store: WindowsSessionStore('session_v1'),
        transport: transport,
      );
    };
    return controller;
  }

  Future<List<InternetAddress>?> _resolveControlHost(String host) async {
    if (actualMode != 'tun') return null;
    final native = _native;
    final currentApi = source is ApiV2DataSource
        ? (source as ApiV2DataSource).environment
        : apiUrl;
    if (host != Uri.parse(currentApi).host || native == null) {
      throw const ApiFailure('control_dns_failed');
    }
    final response = await native.request('ResolveControlHost', host: host);
    final snapshot = response['snapshot'];
    final addresses = snapshot is Map ? snapshot['controlAddresses'] : null;
    if (response['ok'] != true ||
        addresses is! List ||
        addresses.isEmpty ||
        addresses.length > 8) {
      throw const ApiFailure('control_dns_failed');
    }
    return addresses.map((value) {
      if (value is! String || !domain.isPublicIpv4(value)) {
        throw const ApiFailure('control_dns_failed');
      }
      return InternetAddress(value);
    }).toList();
  }

  CustomerDataSource source;
  Future<void> Function()? _configureApi;
  final bool demo;
  final NativeClient Function()? nativeFactory;
  final SuccessfulResourceHistory? history;
  final ExpiryReminderCoordinator? reminders;
  final SessionStore? updateResumeStore;
  final int installedBuild;
  final CrashReportCoordinator? crashReports;
  final DiagnosticClient _diagnosticClient = DiagnosticClient();
  VoidCallback? _disposeTransport;
  NativeClient? _native;
  bool _networkChecked = false;
  Future<bool>? _disconnecting;
  StreamSubscription<Map<String, dynamic>>? _nativeEvents;
  Timer? _nativePoll;
  bool _polling = false;
  String? localProxyAddress;
  domain.ControlContext? _nativeContext;
  domain.TrafficSnapshot? _previousTraffic;
  domain.TrafficView traffic = const domain.TrafficView(
    available: false,
    complete: false,
  );
  final Future<String> Function() _getDeviceId;
  final Future<Preferences> Function() _readPreferences;
  final Future<void> Function(Preferences) _savePreferences;
  Preferences preferences = const Preferences();
  Preferences _savedPreferences = const Preferences();
  Customer? customer;
  List<domain.NodeRecord> nodes = [];
  domain.NodeRecord? selected;
  bool initializing = true, busy = false, loading = false, hasMore = false;
  bool storageReady = false;
  bool logoutNeedsRetry = false;
  bool restoreNeedsRetry = false;
  bool _starting = false;
  String? cursor, message, actualExitIp;
  String connection = 'idle';
  String actualMode = 'none';
  DateTime? syncedAt;
  final List<String> events = [];
  List<({String name, bool? passed, String detail})> diagnostics = [];
  int _generation = 0, _attempt = 0;
  bool _disposed = false;
  String? _localUser;
  bool _automaticAttempted = false;
  Timer? _resourceTimer;
  Timer? _summaryTimer;
  int _summaryRetries = 0;
  bool _syncingAccount = false;
  bool _refreshingActive = false;
  domain.NodeRecord? _connectedNode;
  int _resourceRevision = 1;
  Future<void> _preferenceWrites = Future.value();
  Map<String, Object?>? _pendingUpdateResume;
  bool _resumeCancellationPending = false;
  bool get updateUnsupported => _updateUnsupported;
  bool _updateUnsupported = false;
  Future<void> Function()? _beforeAutomaticConnect;
  void setUpdateUnsupported(bool value) {
    _updateUnsupported = value;
    if (value && connecting) {
      ++_attempt;
      final native = _native;
      if (native != null) {
        unawaited(
          _pauseForResourceChange(
            native,
            reason: 'UPDATE-004 · 当前版本不再支持新连接，请先更新。',
          ),
        );
      } else {
        connection = 'failed';
        message = 'UPDATE-004 · 当前版本不再支持新连接，请先更新。';
      }
    }
    _changed();
  }

  void handleDataFailure(Object error) => _handleFailure(error);

  bool get connected => connection == 'connected';
  bool get connecting => connection == 'connecting';
  bool get recoveryRequired =>
      !demo && nativeFactory != null && !_networkChecked;
  bool get active =>
      connected ||
      connecting ||
      connection == 'stopping' ||
      _resumeCancellationPending ||
      _native != null ||
      recoveryRequired;
  bool get canLogin =>
      !active ||
      actualMode == 'tun' &&
          connection == 'paused' &&
          _native?.available == true;
  bool get canRetryProtected =>
      !updateUnsupported &&
      const {'tun', 'system_proxy'}.contains(actualMode) &&
      connection == 'paused' &&
      customer != null &&
      selected != null &&
      _native?.available == true;

  Future<void> retryProtectedConnection() async {
    if (!canRetryProtected || busy) return;
    await _resumeNetwork(_native!, selected!, _attempt, _generation);
  }

  Future<bool> prepareForUpdate() async {
    if (busy || initializing) return false;
    ++_attempt;
    _diagnosticClient.cancel();
    final native = _native;
    if (native == null) return !recoveryRequired;
    final node = _connectedNode;
    if (updateResumeStore != null && node != null) {
      final marker = <String, Object?>{
        'version': 1,
        'phase': 'prepared',
        'build': installedBuild,
        'localUser': _localUser,
        'key': node.key.toJson(),
        'mode': actualMode,
        'logicalSessionId': _nativeContext?.logicalSessionId,
      };
      try {
        await updateResumeStore!.replace(marker);
        _pendingUpdateResume = marker;
      } catch (_) {
        message = 'STORAGE-005 · 无法保存更新恢复状态，请修复本地存储后重试。';
        _changed();
        return false;
      }
    }
    await _pauseForResourceChange(native, reason: '更新准备完成，转发已暂停；现有保护保持生效。');
    return connection == 'paused';
  }

  Future<bool> handoffUpdate() async {
    final native = _native;
    if (native == null) return !recoveryRequired;
    try {
      if (_pendingUpdateResume != null && updateResumeStore != null) {
        final marker = {..._pendingUpdateResume!, 'phase': 'handed_off'};
        await updateResumeStore!.replace(marker);
        _pendingUpdateResume = marker;
      }
      await native.detachForUpdate();
      await _nativeEvents?.cancel();
      _native = null;
      _nativePoll?.cancel();
      return true;
    } catch (_) {
      message = 'PROT-003 · 更新交接未确认，保护尚未释放，请检查安装器或恢复入口。';
      _changed();
      return false;
    }
  }

  String get connectionLabel => switch (connection) {
    'connecting' => '正在验证连接',
    'connected' =>
      demo
          ? '模拟已连接'
          : actualMode == 'tun'
          ? 'TUN 接管已连接'
          : '系统代理已连接',
    'stopping' => '正在断开',
    'pausing' => '正在暂停转发',
    'failed' => '连接未完成',
    'paused' => '连接已暂停',
    _ => '未连接',
  };

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  void _event(String text) {
    events.add('${DateTime.now().toUtc().toIso8601String()} $text');
    if (events.length > 100) events.removeAt(0);
  }

  Future<void> initialize({
    Future<void> Function()? beforeAutomaticConnect,
  }) async {
    if (_starting || busy) return;
    _beforeAutomaticConnect = beforeAutomaticConnect ?? _beforeAutomaticConnect;
    _starting = true;
    initializing = true;
    restoreNeedsRetry = false;
    message = null;
    _changed();
    try {
      try {
        preferences = await _readPreferences();
        if (!DesktopPlatform.current.supportsTun && preferences.mode == 'tun') {
          preferences = preferences.copyWith(mode: 'system_proxy');
        }
        _savedPreferences = preferences;
      } catch (_) {
        preferences = const Preferences();
        _savedPreferences = preferences;
        message = 'STORAGE-002 · 设置无法读取，已使用默认设置；重新保存设置可修复。';
      }
      _localUser = await _getDeviceId();
      storageReady = true;
      try {
        _pendingUpdateResume = await updateResumeStore?.read();
      } catch (_) {
        message = 'STORAGE-005 · 更新恢复记录不可用，现有网络保护仍需检查。';
      }
      if (!demo && nativeFactory != null) await _initializeNetwork();
      if (_configureApi != null) {
        await _configureApi!();
        _configureApi = null;
      }
      customer = await source.restore();
      if (customer != null) {
        await refresh();
        await _syncAccountResources();
        try {
          await _beforeAutomaticConnect?.call();
          if (!await _restoreUpdateSession()) await _automaticConnect();
        } catch (error) {
          // Version gating stops new forwarding, while restored login,
          // protection monitoring and normal resource polling stay available.
          _handleFailure(error);
        }
      }
      _event('启动完成');
    } catch (error) {
      if (storageReady) {
        restoreNeedsRetry =
            error is ApiFailure &&
            (error.transient || error.code == 'retry_later');
        _handleFailure(error);
      } else {
        message = 'STORAGE-001 · 无法读取设备记录，请重试或修复本地存储。';
      }
    } finally {
      // A previous environment's session must not disable periodic checks
      // after the user signs in to the current service.
      if (!demo && storageReady && !_disposed) {
        _resourceTimer ??= Timer.periodic(const Duration(minutes: 1), (_) {
          reminders?.updateDisplayTime(effectiveNowUtc);
          _changed();
          unawaited(refreshActiveResource());
        });
      }
      initializing = false;
      _starting = false;
      _changed();
    }
  }

  DateTime get effectiveNowUtc => source is ApiV2DataSource
      ? (source as ApiV2DataSource).effectiveNowUtc ?? DateTime.now().toUtc()
      : DateTime.now().toUtc();

  domain.ReminderScope? get _accountScope {
    final account = customer;
    if (account == null || _localUser == null) return null;
    final current = selected?.key ?? nodes.firstOrNull?.key;
    final environment = source is ApiV2DataSource
        ? (source as ApiV2DataSource).environment
        : current?.environment;
    final dataSource = source is ApiV2DataSource
        ? 'api_v2'
        : current?.dataSource;
    if (environment == null || dataSource == null) return null;
    return domain.ReminderScope(
      localUser: _localUser!,
      environment: environment,
      dataSource: dataSource,
      accountKey: account.id,
    );
  }

  Future<void> _automaticConnect() async {
    if (_automaticAttempted ||
        updateUnsupported ||
        !preferences.autoConnect ||
        active ||
        customer == null) {
      return;
    }
    _automaticAttempted = true;
    final scope = _accountScope;
    if (history == null || scope == null) return;
    final generation = _generation;
    try {
      final key = await history!.getLast(scope);
      if (_disposed || generation != _generation || active) return;
      if (key == null) {
        message = 'AUTO-001 · 尚无成功连接记录，请先手动连接一次。';
        return;
      }
      final fresh = await source.getNode(key.sourceId);
      if (_disposed || generation != _generation || active) return;
      if (fresh.key != key) {
        throw const ApiFailure('resource_identity_mismatch');
      }
      selected = fresh;
      await connect(automatic: true);
    } catch (error) {
      if (generation == _generation) _handleFailure(error);
    }
  }

  Future<void> _recordSuccess(String id) async {
    final scope = _accountScope;
    final key = selected?.key;
    if (history == null || scope == null || key == null) return;
    try {
      await history!.recordSuccess(scope, key, successId: id);
    } catch (_) {
      message = 'STORAGE-004 · 已连接，但成功连接记录未能保存。';
    }
  }

  Future<bool> _restoreUpdateSession() async {
    final marker = _pendingUpdateResume;
    if (marker == null || customer == null || marker['phase'] != 'handed_off') {
      return false;
    }
    final generation = _generation, attempt = _attempt;
    try {
      final key = domain.ResourceKey.fromJson(
        Map<String, dynamic>.from(marker['key'] as Map),
      );
      final scope = _accountScope;
      if (marker['version'] != 1 ||
          marker['phase'] != 'handed_off' ||
          marker['localUser'] != _localUser ||
          scope == null ||
          !scope.contains(key) ||
          marker['mode'] != preferences.mode ||
          marker['build'] is! int ||
          (marker['build'] as int) < 1) {
        return false;
      }
      // A ready package, failed launch, or restart of this same version does
      // not constitute a completed upgrade or permission to resume.
      if ((marker['build'] as int) >= installedBuild) return true;
      if (marker['mode'] == 'tun' &&
          (actualMode != 'tun' || _native?.available != true)) {
        // Explicit release removes the native protection intent. A remaining
        // GUI record alone cannot resurrect that released TUN session.
        return true;
      }
      final fresh = await source.getNode(key.sourceId);
      if (_disposed || generation != _generation || attempt != _attempt) {
        return true;
      }
      if (fresh.key != key ||
          domain.prepareProfile(fresh, effectiveNowUtc) is! domain.Ready) {
        message = 'UPDATE-003 · 原资源当前不具备连接资格，更新后保持暂停。';
        return true;
      }
      selected = fresh;
      _connectedNode = fresh;
      if (actualMode == 'tun' && _native != null) {
        connection = 'connecting';
        await _connectNative(fresh, ++_attempt, generation, reuse: _native);
      } else if (!active) {
        await connect();
      }
      if (connected && selected?.key == key) {
        await updateResumeStore?.clear();
        _pendingUpdateResume = null;
        _event('更新后原资源已重新连接');
      }
    } catch (error) {
      if (generation != _generation) return true;
      if (_invalidSession(error)) {
        _handleFailure(error);
      } else {
        final native = _native;
        if (native != null) {
          await _pauseForResourceChange(
            native,
            reason: 'UPDATE-003 · 更新后未能恢复原资源，现有保护保留，请重试或修复。',
          );
        } else {
          message = 'UPDATE-003 · 更新后未能恢复原资源，请重新连接。';
        }
      }
    }
    return true;
  }

  Future<void> _syncAccountResources() async {
    if (_disposed ||
        customer == null ||
        _syncingAccount ||
        source is! ResourceQueryDataSource) {
      return;
    }
    final scope = _accountScope;
    if (scope == null) return;
    final generation = _generation;
    _syncingAccount = true;
    _summaryTimer?.cancel();
    Duration? retryAfter;
    try {
      final capabilities = await source.refreshCapabilities();
      if (_disposed || generation != _generation) return;
      if (reminders == null || !capabilities.expirySummaries) {
        _summaryRetries = 0;
        return;
      }
      final round = newId();
      final summaries = <domain.ExpirySummary>[];
      var complete = capabilities.allOwnedResources;
      for (final kind in ['subscription', 'asset']) {
        String? next;
        final seen = <String>{};
        for (var pageNo = 0; pageNo < 100; pageNo++) {
          final page = await source.listExpirySummaries(
            kind: kind,
            cursor: next,
          );
          if (_disposed || generation != _generation) return;
          complete = complete && page.snapshotComplete;
          for (final row in page.items) {
            final item = row.summary;
            if (!scope.contains(item.key)) {
              throw const ApiFailure('resource_identity_mismatch');
            }
            summaries.add(
              domain.ExpirySummary(
                key: item.key,
                name: item.name,
                customerRemark: item.customerRemark,
                expiresAtUtc: item.expiresAtUtc,
                owned: item.owned,
              ),
            );
          }
          if (!page.hasMore) break;
          next = page.nextCursor;
          if (next == null || !seen.add(next)) {
            throw const ApiFailure('invalid_response');
          }
          if (pageNo == 99) complete = false;
        }
      }
      if (_disposed || generation != _generation) return;
      await reminders!.synchronize(
        domain.ExpirySnapshot(
          scope: scope,
          items: summaries,
          complete: complete,
          syncGeneration: round,
        ),
        effectiveNowUtc: effectiveNowUtc,
        endOfRound: true,
      );
      _summaryRetries = 0;
    } catch (error) {
      if (generation == _generation) {
        if (_invalidSession(error)) {
          _handleFailure(error);
        } else {
          _event('到期摘要同步未完成');
          if (error is ApiFailure && error.transient && _summaryRetries < 3) {
            final backoff = Duration(minutes: 1 << _summaryRetries++);
            final requested = error.retryAfter ?? Duration.zero;
            retryAfter = requested > backoff
                ? Duration(seconds: requested.inSeconds.clamp(1, 1800))
                : backoff;
          }
        }
      }
    } finally {
      _syncingAccount = false;
      if (!_disposed && customer != null && !demo) {
        _summaryTimer = Timer(
          generation != _generation
              ? Duration.zero
              : retryAfter ?? Duration(seconds: 1500 + Random().nextInt(601)),
          () => unawaited(_syncAccountResources()),
        );
      }
      _changed();
    }
  }

  Future<void> _initializeNetwork() async {
    try {
      final existing = _native;
      final reuse = existing?.available == true;
      final native = reuse ? existing! : nativeFactory!();
      _native = native;
      final hello = reuse
          ? await native.status()
          : await native.start(mode: preferences.mode);
      final snapshot = hello['snapshot'] as Map<String, dynamic>;
      _monitorNative(native);
      _adoptProtection(snapshot);
      if (snapshot['protectionIntent'] == true &&
          snapshot['actualMode'] == 'tun') {
        actualMode = 'tun';
        connection = 'paused';
        _networkChecked = false;
        message = 'PROT-003 · 上次 TUN 接管的保护仍在生效，请主动恢复网络后重新连接。';
        return;
      }
    } catch (_) {
      // Recovery uses a fresh transport after terminal native failure.
    }
    await disconnect();
  }

  /// This is an authoritative detail read. Missing items on a list page never
  /// revoke a connection; explicit unavailable/forbidden detail responses do.
  Future<void> refreshActiveResource() async {
    final previous = _connectedNode, native = _native;
    if (!connected || previous == null || native == null || _refreshingActive) {
      return;
    }
    final generation = _generation, attempt = _attempt;
    _refreshingActive = true;
    try {
      final fresh = await source.getNode(previous.key.sourceId);
      if (_disposed || generation != _generation || attempt != _attempt) return;
      final valid =
          fresh.key == previous.key &&
          fresh.isForwarded &&
          domain.prepareProfile(fresh, effectiveNowUtc) is domain.Ready;
      final unchanged =
          fresh.protocol == previous.protocol &&
          fresh.host == previous.host &&
          fresh.port == previous.port &&
          fresh.username == previous.username &&
          fresh.password == previous.password &&
          fresh.protocolConfig == previous.protocolConfig;
      if (!valid || !unchanged) {
        await _pauseForResourceChange(native);
        return;
      }
      final response = await native.request(
        'ApplyResourceUpdate',
        serverTimeUtc: source is ApiV2DataSource
            ? (source as ApiV2DataSource).effectiveNowUtc
            : null,
        update: {
          'revision': ++_resourceRevision,
          'validUntilUtc': fresh.expiresAtUtc?.toUtc().toIso8601String(),
          'unavailable': false,
        },
      );
      if (_disposed || generation != _generation || attempt != _attempt) return;
      if (response['ok'] != true) {
        throw const NativeClientException('NATIVE_REJECTED');
      }
      _connectedNode = fresh;
      if (selected?.key == fresh.key) selected = fresh;
    } on ApiFailure catch (error) {
      if (generation != _generation || attempt != _attempt) return;
      if (_invalidSession(error)) {
        _handleFailure(error);
      } else if (error.code == 'node_unavailable' ||
          error.status == 403 ||
          error.status == 404) {
        await _pauseForResourceChange(native);
      } else {
        _event('资源详情暂时无法同步，继续执行已知期限');
      }
    } catch (_) {
      if (generation == _generation && attempt == _attempt) {
        await _pauseForResourceChange(native);
      }
    } finally {
      _refreshingActive = false;
      _changed();
    }
  }

  Future<void> _pauseForResourceChange(
    NativeClient native, {
    String? reason,
  }) async {
    if (!identical(_native, native)) return;
    connection = 'pausing';
    actualExitIp = null;
    traffic = const domain.TrafficView(available: false, complete: false);
    try {
      final response = await native.request('Pause');
      if (!identical(_native, native) || connection != 'pausing') return;
      final snapshot = response['snapshot'];
      if (response['ok'] != true ||
          snapshot is! Map ||
          snapshot['businessGateOpen'] != false ||
          snapshot['gateKnown'] != true) {
        throw const NativeClientException('NATIVE_REJECTED');
      }
      connection = 'paused';
      _adoptProtection(snapshot);
      message = reason ?? 'ELIG-001 · 资源资格或配置已变化，已暂停转发；请刷新后重连或主动恢复网络。';
    } catch (_) {
      if (!identical(_native, native) || connection != 'pausing') return;
      connection = 'failed';
      _networkChecked = false;
      message = 'PROT-003 · 暂停状态未确认，请使用恢复入口检查网络保护。';
    }
    _changed();
  }

  Future<void> resetLocalStorage() async {
    if (initializing || busy || active) return;
    busy = true;
    _changed();
    try {
      // User explicitly confirms this recovery in the login screen.
      for (final key in ['session_v1', 'device_id', 'preferences']) {
        await platform.invokeMethod<void>('deleteRecord', {'key': key});
      }
      logoutNeedsRetry = false;
    } catch (_) {
      message = 'STORAGE-003 · 本地存储修复失败，请检查当前用户目录权限。';
      busy = false;
      _changed();
      return;
    }
    busy = false;
    await initialize();
  }

  static bool _invalidSession(Object error) =>
      error is ApiFailure &&
      (error.terminal ||
          const {
            'not_authenticated',
            'refresh_retry_expired',
            'stale_refresh',
            'invalid_session',
          }.contains(error.code));

  void _handleFailure(Object error) {
    if (_invalidSession(error)) {
      final native = _native;
      // Expired authentication is not an explicit request to release protection.
      if (native != null &&
          (actualMode == 'tun' || preferences.mode == 'tun')) {
        unawaited(
          _pauseForResourceChange(
            native,
            reason: 'AUTH-002 · 会话已失效，已暂停转发并保留保护，请重新登录。',
          ),
        );
      } else if (native != null || recoveryRequired) {
        unawaited(disconnect());
      }
      ++_generation;
      ++_attempt;
      _summaryTimer?.cancel();
      _summaryRetries = 0;
      unawaited(
        reminders?.clearAccount().catchError((Object _) {
          _event('失效账号的提醒清理未完成');
        }),
      );
      customer = null;
      nodes = [];
      selected = null;
      cursor = null;
      hasMore = false;
      loading = false;
      busy = false;
      if (_native == null && !recoveryRequired && _disconnecting == null) {
        connection = 'idle';
      }
      actualExitIp = null;
      restoreNeedsRetry = false;
      message = 'AUTH-002 · 会话已失效，请重新登录。';
    } else {
      message = errorMessage(error);
    }
    _changed();
  }

  Future<void> login(String username, String password) async {
    if (busy || initializing || !canLogin) return;
    if (logoutNeedsRetry) return;
    if (!storageReady) {
      message = 'STORAGE-001 · 安全存储不可用，无法保存会话。';
      _changed();
      return;
    }
    final generation = ++_generation;
    _summaryRetries = 0;
    busy = true;
    message = null;
    _changed();
    try {
      final result = await source.login(
        username: username.trim(),
        password: password,
        deviceName: 'BlueGrass ${DesktopPlatform.current.name}',
        deviceId: await _getDeviceId(),
      );
      if (_disposed || generation != _generation) return;
      customer = result;
      nodes = [];
      selected = null;
      cursor = null;
      _event('登录成功');
      await refresh();
      await _syncAccountResources();
    } catch (error) {
      if (generation == _generation) _handleFailure(error);
    } finally {
      if (generation == _generation) {
        busy = false;
        _changed();
      }
    }
  }

  Future<void> refresh({bool more = false}) async {
    if (customer == null || loading || more && !hasMore) return;
    final generation = _generation;
    loading = true;
    message = null;
    _changed();
    try {
      var page = await source.listNodes(cursor: more ? cursor : null);
      if (_disposed || generation != _generation) {
        return;
      }
      final merged = <domain.ResourceKey, domain.NodeRecord>{
        if (more)
          for (final node in nodes) node.key: node,
        for (final node in page.items) node.key: node,
      };
      // Initial empty/ineligible pages do not establish that no connectable
      // resource exists. Follow bounded cursors until the first valid choice.
      final seenCursors = <String>{};
      if (more && cursor != null) seenCursors.add(cursor!);
      var fetchedPages = 1;
      while (selected == null &&
          page.hasMore &&
          fetchedPages < 100 &&
          !merged.values.any(
            (n) => domain.prepareProfile(n, effectiveNowUtc) is domain.Ready,
          )) {
        final next = page.nextCursor;
        if (next == null || !seenCursors.add(next)) {
          throw const ApiFailure('invalid_response');
        }
        page = await source.listNodes(cursor: next);
        if (_disposed || generation != _generation) return;
        for (final node in page.items) {
          merged[node.key] = node;
        }
        fetchedPages++;
      }
      nodes = merged.values.toList();
      cursor = page.nextCursor;
      hasMore = page.hasMore;
      syncedAt = DateTime.now().toUtc();
      final scope = _accountScope;
      if (history != null && scope != null) {
        try {
          final recent = await history!.getRecent(scope);
          if (_disposed || generation != _generation) return;
          final rank = {for (final e in recent.indexed) e.$2: e.$1};
          final ordered = nodes.indexed.toList()
            ..sort((a, b) {
              final r = (rank[a.$2.key] ?? recent.length).compareTo(
                rank[b.$2.key] ?? recent.length,
              );
              return r != 0 ? r : a.$1.compareTo(b.$1);
            });
          nodes = ordered.map((e) => e.$2).toList();
        } catch (_) {
          _event('最近成功顺序读取未完成');
        }
      }
      if (_disposed || generation != _generation) return;
      if (selected == null) {
        selected = nodes
            .where(
              (n) => domain.prepareProfile(n, effectiveNowUtc) is domain.Ready,
            )
            .firstOrNull;
      } else {
        selected =
            nodes.where((n) => n.key == selected!.key).firstOrNull ?? selected;
      }
      _event('资源同步成功');
    } catch (error) {
      if (generation == _generation) _handleFailure(error);
    } finally {
      if (generation == _generation) {
        loading = false;
        _changed();
      }
    }
  }

  Future<void> select(domain.NodeRecord node) async {
    if (selected?.key == node.key) return;
    final native = _native;
    if (!demo &&
        native != null &&
        native.available &&
        const {'tun', 'system_proxy'}.contains(actualMode)) {
      ++_attempt;
      _diagnosticClient.cancel();
      await _pauseForResourceChange(
        native,
        reason: '原资源已暂停，现有网络保护与设置保留；请验证并连接所选资源。',
      );
      if (connection != 'paused') return;
      selected = node;
      actualExitIp = null;
      _changed();
      return;
    }
    if (active && !await disconnect()) return;
    selected = node;
    actualExitIp = null;
    connection = 'idle';
    message = null;
    _changed();
  }

  Future<void> connect({bool automatic = false}) async {
    if (!DesktopPlatform.current.supportsTun && preferences.mode == 'tun') {
      message = DesktopPlatform.tunUnavailable;
      _changed();
      return;
    }
    if (updateUnsupported) {
      message = 'UPDATE-004 · 当前版本不再支持新连接，请先更新。';
      _changed();
      return;
    }
    final node = selected;
    if (node == null || active || customer == null || busy) return;
    final attempt = ++_attempt, generation = _generation;
    connection = 'connecting';
    actualExitIp = null;
    message = null;
    _changed();
    try {
      await _beforeAutomaticConnect?.call();
      final current = await source.getNode(node.key.sourceId);
      if (_disposed || attempt != _attempt || generation != _generation) return;
      if (current.key != node.key) {
        throw const ApiFailure('resource_identity_mismatch');
      }
      selected = current;
      final prepared = domain.prepareProfile(current, effectiveNowUtc);
      if (prepared is domain.Rejected) {
        message = switch (prepared.error.code) {
          'SYNC-003' => 'SYNC-003 · 资源信息无效，请刷新后重试。',
          'ELIG-003' => 'ELIG-003 · 资源已到期，请在官网续费后刷新。',
          'ELIG-008' => '当前 Xray 内核不支持此协议，请选择其他资源。',
          _ => '${prepared.error.code} · 资源当前不能连接，请刷新资源。',
        };
        connection = 'failed';
        _event('连接被资格检查拒绝');
        return;
      }
      if (!demo) {
        if (nativeFactory == null) throw const ApiFailure('native_unavailable');
        await _connectNative(current, attempt, generation);
        return;
      }
      // Explicit fixture build only. This does not claim any OS networking action.
      await Future<void>.delayed(const Duration(milliseconds: 250));
      if (_disposed || attempt != _attempt || generation != _generation) return;
      final expiry = current.expiresAtUtc;
      if (expiry != null && !DateTime.now().toUtc().isBefore(expiry)) {
        throw const ApiFailure('node_unavailable');
      }
      connection = 'connected';
      actualExitIp = null; // The preview performs no outbound observation.
      // Keep the API cursor intact while showing a successful recent resource first.
      nodes = [current, ...nodes.where((n) => n.key != current.key)];
      _event('模拟连接流程完成');
      await _recordSuccess('fixture-$generation-$attempt');
    } catch (error) {
      if (attempt == _attempt && generation == _generation) {
        final native = _native;
        if (error is NativeClientException &&
            actualMode == 'tun' &&
            native?.available == true) {
          await _pauseForResourceChange(native!, reason: errorMessage(error));
        } else {
          connection = 'failed';
          _handleFailure(error);
        }
        _event('连接未完成');
      }
    } finally {
      _changed();
    }
  }

  Future<bool> disconnect() =>
      _disconnecting ??= _disconnect().whenComplete(() {
        _disconnecting = null;
      });

  Future<bool> _disconnect() async {
    _diagnosticClient.cancel();
    ++_attempt;
    if (!initializing && _pendingUpdateResume != null) {
      try {
        await updateResumeStore?.clear();
        _pendingUpdateResume = null;
        _resumeCancellationPending = false;
      } catch (_) {
        try {
          // Some stores can atomically replace a record even when its delete
          // fails. Persist cancellation without any resource/session data.
          await updateResumeStore?.replace({
            'version': 1,
            'phase': 'cancelled',
          });
          _pendingUpdateResume = null;
          _resumeCancellationPending = false;
        } catch (_) {
          _resumeCancellationPending = true;
          _event('更新恢复意图撤销未确认');
        }
      }
    }
    _nativePoll?.cancel();
    _nativePoll = null;
    actualExitIp = null;
    connection = _native == null && !recoveryRequired ? 'idle' : 'stopping';
    _changed();
    var preserved = false;
    for (var attempt = 0; attempt < 2; attempt++) {
      var native = _native;
      if (native == null && !recoveryRequired) break;
      try {
        if (native == null) {
          native = nativeFactory!();
          _native = native;
          await native.start(mode: preferences.mode);
        }
        await native.stop();
        preserved = native.externalProxyPreserved;
        _networkChecked = true;
        _native = null;
        break;
      } catch (_) {
        // A failed transport cannot be reused. Wait for its cleanup before
        // one fresh host attempts recovery independently of resource eligibility.
        try {
          await native?.stop();
        } catch (_) {
          /* Remains unconfirmed. */
        }
        _native = null;
        _networkChecked = false;
        if (attempt == 0) continue;
        connection = 'failed';
        message = 'PROT-003 · 系统代理恢复未确认，请点击“恢复并断开代理”重试。';
        _changed();
        return false;
      }
    }
    await _nativeEvents?.cancel();
    _nativeEvents = null;
    localProxyAddress = null;
    _nativeContext = null;
    _connectedNode = null;
    _previousTraffic = null;
    traffic = const domain.TrafficView(available: false, complete: false);
    connection = 'idle';
    actualMode = 'none';
    _event(
      demo
          ? '模拟连接已断开'
          : preserved
          ? '代理已释放，保留其他程序当前设置'
          : '代理已停止，系统设置已恢复',
    );
    _changed();
    if (_resumeCancellationPending) {
      message = 'STORAGE-005 · 网络已恢复，但更新恢复意图未能撤销；请修复本地存储并重试。';
      _changed();
      return false;
    }
    return true;
  }

  Future<void> _connectNative(
    domain.NodeRecord node,
    int attempt,
    int generation, {
    NativeClient? reuse,
  }) async {
    await _beforeAutomaticConnect?.call();
    if (updateUnsupported) throw const ApiFailure('client_upgrade_required');
    if (_native != null && reuse == null) await _native!.stop();
    if (_disposed || attempt != _attempt || generation != _generation) return;
    final native = reuse ?? nativeFactory!();
    if (reuse == null) {
      _nativeContext = null;
      _connectedNode = null;
    }
    _native = native;
    _networkChecked = false;
    final hello = await native.start(mode: preferences.mode);
    _adoptProtection(hello['snapshot']);
    _monitorNative(native);
    if (_disposed || attempt != _attempt || generation != _generation) return;
    final switching =
        reuse != null &&
        _nativeContext != null &&
        _nativeContext!.resourceKey != node.key;
    final reply = await native.request(
      switching ? 'SwitchResource' : 'Connect',
      profile: {
        'key': node.key.toJson(),
        'protocol': node.protocol,
        'entry': {'host': node.host, 'port': node.port},
        'auth': {'username': node.username, 'password': node.password},
        if (node.protocolConfig != null)
          'protocolConfig': node.protocolConfig!.toJson(),
        'validUntilUtc': node.expiresAtUtc?.toUtc().toIso8601String(),
        'configVersion': 1,
        'configFingerprint': newId(),
        'mode': preferences.mode,
        'routingMode': preferences.routingMode,
      },
      allowLan: preferences.allowLan,
      entryDns: preferences.entryDns.toJson(),
      serverTimeUtc: source is ApiV2DataSource
          ? (source as ApiV2DataSource).effectiveNowUtc
          : null,
    );
    _adoptProtection(reply['snapshot']);
    // An accepted attempt owns its resource before forwarding is ready.
    // Keep that identity separate from the last connected resource. The IPC
    // boundary also validates the explicit protected/contextless recovery case.
    final snapshot = reply['snapshot'];
    if (identical(_native, native) && snapshot is Map<String, dynamic>) {
      final context = snapshot['context'];
      _nativeContext = context is Map<String, dynamic>
          ? domain.ControlContext.fromJson(context)
          : null;
      if (reply['ok'] == true || _nativeContext == null) {
        _connectedNode = null;
        _previousTraffic = null;
        traffic = const domain.TrafficView(available: false, complete: false);
      }
    }
    if (reply['ok'] != true) {
      throw NativeClientException(reply['error'] as String);
    }
    await _awaitConnected(native, node, attempt, generation);
  }

  // Protection can already be armed while Prepare or connection setup fails.
  // This says nothing about successful forwarding; it selects the authenticated
  // service DNS path and keeps recovery/keepalive available.
  void _adoptProtection(dynamic snapshot) {
    if (snapshot is Map &&
        snapshot['actualMode'] == 'tun' &&
        snapshot['protectionIntent'] == true) {
      actualMode = 'tun';
      _networkChecked = false;
    }
  }

  void _monitorNative(NativeClient native) {
    unawaited(_nativeEvents?.cancel());
    _nativeEvents = native.events.listen((event) {
      if (event['event'] == 'crash' && identical(_native, native)) {
        unawaited(
          crashReports?.record(
            const CrashSignal(
              module: CrashModule.nativeBroker,
              code: CrashCode.nativeCrash,
              frames: [CrashFrame.nativeReadLoop],
            ),
          ),
        );
      }
      if (event['event'] == 'failure' &&
          identical(_native, native) &&
          connection != 'stopping') {
        actualExitIp = null;
        connection = 'failed';
        traffic = domain.TrafficView(
          available: false,
          complete: false,
          uploadBytes: traffic.uploadBytes,
          downloadBytes: traffic.downloadBytes,
        );
        _nativePoll?.cancel();
        _networkChecked = false;
        message = 'PROT-003 · 代理进程不可用，请恢复并断开代理后重试。';
        _changed();
      }
    });
    _nativePoll?.cancel();
    _nativePoll = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!connecting && connection != 'pausing' && connection != 'stopping') {
        unawaited(_pollNative(native, _attempt, _generation));
      }
    });
  }

  Future<void> _awaitConnected(
    NativeClient native,
    domain.NodeRecord node,
    int attempt,
    int generation,
  ) async {
    final deadline = DateTime.now().add(const Duration(seconds: 40));
    while (DateTime.now().isBefore(deadline)) {
      if (_disposed || attempt != _attempt || generation != _generation) return;
      final status = await native.status();
      if (_disposed || attempt != _attempt || generation != _generation) return;
      if (status['ok'] != true) {
        throw const NativeClientException('NATIVE_REJECTED');
      }
      final snapshot = status['snapshot'] as Map<String, dynamic>;
      _adoptProtection(snapshot);
      if (snapshot['connection'] == 'connected') {
        final context = domain.ControlContext.fromJson(
          snapshot['context'] as Map<String, dynamic>,
        );
        if (context != _nativeContext ||
            context.resourceKey != node.key ||
            snapshot['gateKnown'] != true ||
            snapshot['businessGateOpen'] != true ||
            snapshot['actualMode'] != preferences.mode ||
            !_modeApplied(snapshot) ||
            snapshot['proxyRecoveryRequired'] == true) {
          await native.request('Pause');
          throw const NativeClientException('NATIVE_CONTEXT_MISMATCH');
        }
        final address = snapshot['proxyAddress'];
        if (preferences.mode == 'system_proxy' &&
            (address is! String ||
                !RegExp(r'^127\.0\.0\.1:[0-9]+$').hasMatch(address) ||
                address.endsWith(':10808'))) {
          await native.request('Pause');
          throw const NativeClientException('NATIVE_PROTOCOL_ERROR');
        }
        _nativeContext = context;
        _connectedNode = node;
        _resourceRevision = 1;
        connection = 'connected';
        actualMode = preferences.mode;
        _readObservedExit(snapshot);
        localProxyAddress = address is String && address.isNotEmpty
            ? address
            : null;
        nodes = [node, ...nodes.where((n) => n.key != node.key)];
        _readTraffic(snapshot);
        _event(actualMode == 'tun' ? 'TUN 接管与保护已启用' : '系统代理已启用');
        await _recordSuccess(
          '${context.runtimeEpoch}/${context.logicalSessionId}/${context.attemptId}',
        );
        _changed();
        return;
      }
      if (snapshot['connection'] == 'failed' ||
          snapshot['connection'] == 'paused') {
        throw const NativeClientException('ENGINE_FAILURE');
      }
      await Future<void>.delayed(const Duration(milliseconds: 200));
    }
    await native.request('Pause');
    throw const NativeClientException('NATIVE_TIMEOUT');
  }

  Future<void> _resumeNetwork(
    NativeClient native,
    domain.NodeRecord node,
    int oldAttempt,
    int generation,
  ) async {
    if (updateUnsupported) {
      await _pauseForResourceChange(
        native,
        reason: 'UPDATE-004 · 当前版本不再支持新连接，请先更新。',
      );
      return;
    }
    if (_disposed || _attempt != oldAttempt || _generation != generation) {
      return;
    }
    final attempt = ++_attempt;
    connection = 'connecting';
    message = 'CONN-005 · 正在重新检查并连接所选资源；现有保护与设置保留。';
    _changed();
    for (final delay in [1, 2, 5]) {
      await Future<void>.delayed(Duration(seconds: delay));
      if (_disposed ||
          _attempt != attempt ||
          _generation != generation ||
          !identical(_native, native)) {
        return;
      }
      try {
        final fresh = await source.getNode(node.key.sourceId);
        if (_disposed || _attempt != attempt || _generation != generation) {
          return;
        }
        if (fresh.key != node.key ||
            domain.prepareProfile(fresh, effectiveNowUtc) is! domain.Ready) {
          await _pauseForResourceChange(native);
          return;
        }
        selected = fresh;
        await _connectNative(fresh, attempt, generation, reuse: native);
        return;
      } catch (error) {
        if (_attempt != attempt || _generation != generation) return;
        if (_invalidSession(error)) {
          _handleFailure(error);
          return;
        }
      }
    }
    connection = 'paused';
    message = 'CONN-005 · 重新连接未完成，现有保护与设置保留，请重试或主动恢复网络。';
    _changed();
  }

  Future<void> _pollNative(
    NativeClient native,
    int attempt,
    int generation,
  ) async {
    if (_polling ||
        _disposed ||
        !identical(_native, native) ||
        attempt != _attempt ||
        generation != _generation) {
      return;
    }
    _polling = true;
    try {
      final response = await native.status();
      if (_disposed || attempt != _attempt || generation != _generation) return;
      final snapshot = response['snapshot'] as Map<String, dynamic>;
      _adoptProtection(snapshot);
      if (!connected) {
        if (response['ok'] != true ||
            snapshot['gateKnown'] != true ||
            snapshot['businessGateOpen'] != false) {
          await _pauseForResourceChange(native);
        }
        return;
      }
      final context = snapshot['context'] == null
          ? null
          : domain.ControlContext.fromJson(
              snapshot['context'] as Map<String, dynamic>,
            );
      if (response['ok'] != true ||
          context != _nativeContext ||
          snapshot['connection'] != 'connected' ||
          snapshot['gateKnown'] != true ||
          snapshot['businessGateOpen'] != true ||
          !_modeApplied(snapshot) ||
          snapshot['proxyRecoveryRequired'] == true) {
        actualExitIp = null;
        connection = 'paused';
        traffic = const domain.TrafficView(available: false, complete: false);
        message = 'CONN-004 · 连接状态已变化，请断开后重试。';
        if (snapshot['errorCode'] == 'NETWORK_CHANGED' &&
            _connectedNode != null &&
            actualMode == 'tun') {
          unawaited(
            _resumeNetwork(native, _connectedNode!, attempt, generation),
          );
        }
      } else {
        _readObservedExit(snapshot);
        _readTraffic(snapshot);
      }
      _changed();
    } catch (_) {
      if (attempt == _attempt) {
        actualExitIp = null;
        connection = 'failed';
        message = 'PROT-003 · 本地代理状态未知，请断开后重试。';
        _nativePoll?.cancel();
        _changed();
      }
    } finally {
      _polling = false;
    }
  }

  bool _modeApplied(Map<String, dynamic> snapshot) => preferences.mode == 'tun'
      ? snapshot['actualMode'] == 'tun' &&
            snapshot['protectionIntent'] == true &&
            snapshot['protection'] == 'wfp_forwarding' &&
            snapshot['systemProxyApplied'] == false
      : snapshot['actualMode'] == 'system_proxy' &&
            snapshot['systemProxyApplied'] == true;

  // Only call after the snapshot's runtime/session/attempt and forwarding
  // state have been validated. Missing/failed observations are advisory.
  void _readObservedExit(Map<String, dynamic> snapshot) {
    final value = snapshot['verifiedExitIpv4'];
    actualExitIp = value is String && domain.isPublicIpv4(value) ? value : null;
  }

  void _readTraffic(Map<String, dynamic> snapshot) {
    try {
      final current = domain.TrafficSnapshot.fromJson(
        snapshot['traffic'] as Map<String, dynamic>,
      );
      if (current.context != _nativeContext) return;
      traffic = domain.deriveTraffic(_previousTraffic, current);
      _previousTraffic = current;
    } catch (_) {
      traffic = const domain.TrafficView(available: false, complete: false);
    }
  }

  Future<void> logout() async {
    if (busy) return;
    final generation = ++_generation;
    busy = true;
    loading = false;
    _summaryTimer?.cancel();
    final scope = _accountScope;
    _changed();
    try {
      if (!await disconnect() || _disposed || generation != _generation) return;
      try {
        await reminders?.clearAccount(deleteLocalData: true);
        if (scope != null) await history?.clearAccount(scope);
      } catch (_) {
        _event('本账号提醒或历史清理未完成');
      }
      if (_disposed || generation != _generation) return;
      try {
        await source.logout();
        if (_disposed || generation != _generation) return;
        logoutNeedsRetry = false;
        message = null;
      } catch (error) {
        if (_disposed || generation != _generation) return;
        logoutNeedsRetry =
            error is! ApiFailure || error.code != 'logout_remote_unconfirmed';
        message = logoutNeedsRetry
            ? 'STORAGE-003 · 未能清除本地会话，请重试。重启后仍可能恢复登录。'
            : 'AUTH-LOGOUT · 已本地退出，未能确认服务端撤销。';
      }
      customer = null;
      nodes = [];
      selected = null;
      cursor = null;
      hasMore = false;
      _event(logoutNeedsRetry ? '本地会话清理失败' : '本地会话已退出');
    } finally {
      if (generation == _generation) {
        busy = false;
        _changed();
      }
    }
  }

  Future<void> updatePreferences(Preferences value) async {
    if (!DesktopPlatform.current.supportsTun && value.mode == 'tun') {
      message = DesktopPlatform.tunUnavailable;
      _changed();
      return;
    }
    if ((active || busy) &&
        (value.mode != preferences.mode ||
            value.routingMode != preferences.routingMode)) {
      message = active ? '请先断开连接，再切换接管或路由模式。' : '当前操作尚未完成，请稍候再切换接管或路由模式。';
      _changed();
      return;
    }
    preferences = value;
    _changed();
    final write = _preferenceWrites.then((_) async {
      await _savePreferences(value);
      _savedPreferences = value;
    });
    _preferenceWrites = write.catchError((Object _) {});
    try {
      await write;
    } catch (_) {
      if (identical(preferences, value)) preferences = _savedPreferences;
      message = 'STORAGE-002 · 设置未能保存，请重试。';
      _changed();
    }
  }

  Future<void> diagnose() async {
    if (busy) return;
    busy = true;
    _changed();
    try {
      final generation = _generation, attempt = _attempt;
      final result = <({String name, bool? passed, String detail})>[
        (
          name: '安全存储',
          passed: storageReady,
          detail: demo ? '模拟存储' : DesktopPlatform.current.storage,
        ),
        (
          name: '当前接管状态',
          passed: demo ? null : connected,
          detail: demo
              ? '模拟环境，未接管系统网络'
              : connected
              ? actualMode == 'tun'
                    ? '服务报告 TUN 与保护已启用；物理抓包验收单独记录'
                    : '${DesktopPlatform.current.name} 系统代理已启用；仅覆盖遵循代理设置的程序'
              : '连接后可检测实际出口与接管状态',
        ),
      ];
      try {
        if (selected != null && customer != null) {
          final node = await source.getNode(selected!.key.sourceId);
          final profile = domain.prepareProfile(node, effectiveNowUtc);
          result.add((
            name: '资源资格与连接配置',
            passed: profile is domain.Ready,
            detail: profile is domain.Ready ? '资源具备有效的连接配置' : '当前资源缺少连接所需的有效配置',
          ));
        } else {
          result.add((name: '资源资格', passed: null, detail: '请先登录并选择资源'));
        }
      } catch (error) {
        if (_invalidSession(error)) {
          _handleFailure(error);
          return;
        }
        result.add((name: '资源同步', passed: false, detail: errorMessage(error)));
      }
      if (_disposed || generation != _generation || attempt != _attempt) return;
      if (!demo) {
        try {
          // This helper has no privileged-network exception. During TUN it must
          // not pretend its ordinary OS sockets are a controlled selected path.
          final request = <String, dynamic>{};
          if (actualMode != 'tun') {
            request['api_base_url'] = source is ApiV2DataSource
                ? (source as ApiV2DataSource).environment
                : apiUrl;
            final node = selected;
            if (node != null) {
              final policy = preferences.entryDns.toJson();
              final candidates = policy['candidates'] as List? ?? [];
              request['entry'] = {
                'host': node.host,
                'port': node.port.toString(),
                'dns_mode': policy['mode'] == 'custom' ? 'custom' : 'system',
                if (policy['mode'] == 'custom')
                  'dns_servers': [
                    for (final c in candidates)
                      {
                        'address':
                            '${(c['address'] as String).contains(':') ? '[${c['address']}]' : c['address']}:${c['port'] ?? 53}',
                        'network': c['transport'],
                      },
                  ],
              };
            }
          }
          final report = connected && _native != null && _nativeContext != null
              ? await _selectedDiagnosticReport(
                  _native!,
                  _nativeContext!,
                  generation,
                  attempt,
                )
              : await _diagnosticClient.run(request);
          if (_disposed || generation != _generation || attempt != _attempt) {
            return;
          }
          const names = {
            'api_health': 'API HTTPS 健康检查',
            'entry_dns': '入口 DNS 实测',
            'entry_tcp': '入口 TCP 实测',
            'proxy_exit': '独立出口诊断',
            'proxy_udp': 'UDP nonce 往返',
            'physical_bypass': '物理网络绑定',
            'ipv6_routing': 'IPv6 路由',
            'wfp_protection': 'WFP 抓包检查',
          };
          for (final item in report['results'] as List) {
            if (item is! Map || !names.containsKey(item['id'])) continue;
            final code = item['code'], status = item['status'];
            if (code is! String ||
                !RegExp(r'^[a-z0-9_]{1,64}$').hasMatch(code) ||
                !const ['passed', 'failed', 'skipped'].contains(status)) {
              throw const FormatException('DIAGNOSTIC_INVALID_REPORT');
            }
            result.add((
              name: names[item['id']]!,
              passed: status == 'skipped' ? null : status == 'passed',
              detail: status == 'skipped' ? '未测 · $code' : '实测 · $code',
            ));
          }
        } catch (_) {
          if (_disposed || generation != _generation || attempt != _attempt) {
            return;
          }
          result.add((
            name: '原生诊断',
            passed: false,
            detail: 'DIAG-001 · 诊断未完成，请检查安装文件或稍后重试',
          ));
        }
      } else {
        result.add((name: 'UDP 可达性', passed: null, detail: '模拟环境，未进行网络诊断'));
      }
      diagnostics = result;
      busy = false;
      _event('本地诊断完成');
      _changed();
    } finally {
      busy = false;
      _changed();
    }
  }

  String diagnosticJson() => const JsonEncoder.withIndent('  ').convert({
    'application': 'BlueGrass',
    'version': clientVersionLabel,
    'platform': DesktopPlatform.current.diagnosticId,
    'fixture': demo,
    'connection': connection,
    'mode': preferences.mode,
    'checks': [
      for (final item in diagnostics)
        {'name': item.name, 'passed': item.passed, 'detail': item.detail},
    ],
    'events': events,
  });

  Future<Map<String, dynamic>> _selectedDiagnosticReport(
    NativeClient native,
    domain.ControlContext context,
    int generation,
    int attempt,
  ) async {
    final clock = Stopwatch()..start();
    var response = await native.request('Diagnose');
    int? jobGeneration;
    while (clock.elapsed < const Duration(seconds: 35)) {
      if (_disposed ||
          generation != _generation ||
          attempt != _attempt ||
          !identical(_native, native) ||
          _nativeContext != context) {
        throw const FormatException('DIAGNOSTIC_CANCELLED');
      }
      final snapshot = response['snapshot'];
      final job = snapshot is Map ? snapshot['diagnostic'] : null;
      if (response['ok'] != true ||
          job is! Map ||
          job['context'] is! Map<String, dynamic> ||
          domain.ControlContext.fromJson(
                job['context'] as Map<String, dynamic>,
              ) !=
              context ||
          job['generation'] is! int ||
          (job['generation'] as int) <= 0) {
        throw const FormatException('DIAGNOSTIC_INVALID_REPORT');
      }
      jobGeneration ??= job['generation'] as int;
      if (job['generation'] != jobGeneration) {
        throw const FormatException('DIAGNOSTIC_CANCELLED');
      }
      if (job['state'] == 'completed') {
        final report = job['report'];
        if (report is! Map<String, dynamic> ||
            report['version'] != 1 ||
            report['generation'] != jobGeneration ||
            report['cancelled'] != false ||
            report['results'] is! List ||
            (report['results'] as List).length > 24) {
          throw const FormatException('DIAGNOSTIC_INVALID_REPORT');
        }
        if (snapshot is! Map<String, dynamic> ||
            snapshot['context'] is! Map<String, dynamic> ||
            domain.ControlContext.fromJson(
                  snapshot['context'] as Map<String, dynamic>,
                ) !=
                context ||
            snapshot['connection'] != 'connected' ||
            snapshot['gateKnown'] != true ||
            snapshot['businessGateOpen'] != true ||
            !_modeApplied(snapshot) ||
            snapshot['proxyRecoveryRequired'] == true) {
          throw const FormatException('DIAGNOSTIC_INVALID_REPORT');
        }
        _readObservedExit(snapshot);
        return report;
      }
      if (job['state'] != 'running') {
        throw const FormatException('DIAGNOSTIC_FAILED');
      }
      await Future<void>.delayed(const Duration(milliseconds: 250));
      if (_disposed ||
          generation != _generation ||
          attempt != _attempt ||
          !identical(_native, native)) {
        throw const FormatException('DIAGNOSTIC_CANCELLED');
      }
      response = await native.status();
    }
    throw const FormatException('DIAGNOSTIC_TIMEOUT');
  }

  static String errorMessage(Object error) {
    if (error is UpdateException) {
      return error.code == 'UPDATE_REQUIRED'
          ? 'UPDATE-004 · 当前版本不再支持新连接，请先更新。'
          : 'UPDATE-005 · 版本支持状态尚未确认，请检查更新后重试；现有保护保持生效。';
    }
    if (error is ApiFailure) {
      return switch (error.code) {
        'invalid_credentials' => 'AUTH-001 · 账号或密码不正确，请重新输入。',
        'environment_mismatch' => 'AUTH-003 · 服务地址已更新，请重新登录。',
        'client_upgrade_required' => 'UPDATE-004 · 当前版本不再支持新连接，请先更新。',
        'network_error' || 'timeout' => 'SYNC-001 · 暂时无法连接服务，请稍后重试。',
        'native_unavailable' => 'PROT-001 · 本地网络服务尚未就绪，无法建立连接。',
        'node_unavailable' => 'ELIG-001 · 当前资源不可连接，请刷新后重试。',
        'rate_limited' => 'SYNC-002 · 请求过于频繁，请稍后重试。',
        _ when error.terminal => 'AUTH-002 · 会话已失效，请重新登录。',
        _ => 'SYNC-003 · 服务响应无法使用，请重试或联系支持。',
      };
    }
    if (error is PlatformException || error is MissingPluginException) {
      return 'STORAGE-001 · ${DesktopPlatform.current.name} 功能不可用，请重启客户端。';
    }
    if (error is NativeClientException) {
      if (DesktopPlatform.current.isMac &&
          const {
            'SERVICE_UNAVAILABLE',
            'NATIVE_START_FAILED',
          }.contains(error.code)) {
        return 'PROT-001 · 本地代理组件不可用，请重新下载并替换完整的 macOS 开发应用。';
      }
      return const {
            'SERVICE_UNAVAILABLE',
            'SERVICE_CALLER_REJECTED',
            'NATIVE_START_FAILED',
          }.contains(error.code)
          ? 'PROT-001 · 网络服务未就绪，请使用签名安装包安装或修复 BlueGrass。'
          : error.code == 'UNSUPPORTED_COMMAND'
          ? 'PROT-001 · TUN 接管尚未启用，请关闭 TUN 模式后重试。'
          : 'CONN-004 · 本地代理验证未完成，请检查资源后重试。';
    }
    return 'SYNC-003 · 操作未完成，请重试。';
  }

  @override
  void dispose() {
    _disposed = true;
    ++_generation;
    ++_attempt;
    _nativePoll?.cancel();
    _resourceTimer?.cancel();
    _summaryTimer?.cancel();
    _diagnosticClient.cancel();
    unawaited(_nativeEvents?.cancel());
    _disposeTransport?.call();
    unawaited(_native?.stop().catchError((Object _) {}));
    super.dispose();
  }
}

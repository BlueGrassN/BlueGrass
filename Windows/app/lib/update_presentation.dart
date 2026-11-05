import 'dart:async';

import 'package:flutter/foundation.dart';

import 'update_client.dart';
import 'update_user_state.dart';

/// UI state only. All release validation, network, cache and installer work is
/// delegated to the existing UpdateClient. Construction performs no I/O.
class UpdatePresentation extends ChangeNotifier {
  UpdatePresentation({
    required this.client,
    required this.pauseBeforeInstall,
    required this.handoffAfterLaunch,
    required this.exitAfterHandoff,
    UpdatePauseStore? pauseStore,
    Timer Function(Duration, void Function())? schedule,
  }) : pauseStore = pauseStore ?? MemoryUpdatePauseStore(),
       _schedule = schedule ?? Timer.new {
    _progress = client.progress.listen((value) {
      if (_disposed ||
          _operation != 'download' ||
          value.candidateId != snapshot?.candidateId) {
        return;
      }
      snapshot = value;
      notifyListeners();
    });
  }
  final UpdateClient client;
  final UpdatePauseStore pauseStore;
  final Timer Function(Duration, void Function()) _schedule;
  static const checkInterval = Duration(hours: 6);
  static const startupPolicyTimeout = Duration(seconds: 10);
  static const startupCheckTimeout = Duration(seconds: 12);
  static const retryDelays = [
    Duration(minutes: 1),
    Duration(minutes: 5),
    Duration(minutes: 30),
  ];
  Timer? _checkTimer, _retryTimer;
  bool _background = false, _pauseLoaded = false, _pauseLoadFailed = false;
  String? _pausedCandidate;
  int _retries = 0;
  int _lifecycleGeneration = 0;
  Future<void>? _loadingPause;
  Future<void>? _startup;
  bool _startupTrusted = false, _startupNetworkChecked = false;
  bool? _verifiedUnsupported;
  String _minimumVersion = '';
  Future<void> _pauseWrites = Future.value();
  final Future<bool> Function() pauseBeforeInstall;
  final Future<bool> Function() handoffAfterLaunch;
  final Future<void> Function() exitAfterHandoff;
  late final StreamSubscription<UpdateSnapshot> _progress;
  UpdateSnapshot? snapshot;
  String? errorCode, _operation, _dismissedCandidate;
  bool _disposed = false, _launchUncertain = false;
  // In-memory authority only. Durable authority remains the helper's signed
  // manifest + DPAPI/floor; GUI preferences never persist this decision.
  bool? get verifiedUnsupported => _verifiedUnsupported;
  bool get busy => _operation != null;
  bool get userPaused =>
      _pausedCandidate != null && _pausedCandidate == snapshot?.candidateId;
  bool get downloading => _operation == 'download';
  bool get installing => _operation == 'install';
  bool get canCheck =>
      !_disposed &&
      !busy &&
      !_launchUncertain &&
      snapshot?.state != UpdateState.launchRequested;
  bool get canDownload =>
      canCheck &&
      const {
        UpdateState.available,
        UpdateState.downloading,
      }.contains(snapshot?.state);
  bool get canInstall => canCheck && snapshot?.state == UpdateState.ready;
  bool get waitingForNetworkConsent => errorCode == 'UPDATE_WAITING_NETWORK';
  bool get readyNoticeVisible =>
      snapshot?.state == UpdateState.ready &&
      snapshot?.candidateId != _dismissedCandidate &&
      !_launchUncertain;

  void dismissReadyNotice() {
    _dismissedCandidate = snapshot?.candidateId;
    _changed();
  }

  Future<void> _loadPause() => _loadingPause ??= () async {
    if (_pauseLoaded || _disposed) return;
    try {
      _pausedCandidate = await pauseStore.read();
      _pauseLoaded = true;
    } catch (_) {
      _pauseLoadFailed = true;
      errorCode = 'UPDATE_PREFERENCE_FAILED';
    }
  }();

  Future<bool> _savePause(String? candidate) async {
    final task = _pauseWrites.then((_) => pauseStore.write(candidate));
    _pauseWrites = task.catchError((Object _) {});
    try {
      await task;
      return true;
    } catch (_) {
      _pauseLoadFailed = true;
      errorCode = 'UPDATE_PREFERENCE_FAILED';
      _changed();
      return false;
    }
  }

  Future<void> startBackground() async {
    if (_disposed || _background) return;
    _background = true;
    _scheduleCheck();
    if (_startup != null) {
      await _startup;
      if (_disposed || !_background) return;
      if (_startupNetworkChecked) {
        if (canDownload) {
          await _loadPause();
          await _downloadAutomatically();
        } else {
          _scheduleRetry();
        }
        return;
      }
    }
    await check();
  }

  /// Re-read the native signed cache before any automatic connection/resume.
  /// A fresh process never trusts a GUI-restored bool. No package transfer or
  /// installation occurs here; ordinary unconfigured first start returns fast.
  Future<void> prepareStartup() => _startup ??= _prepareStartup();

  Future<void> _prepareStartup() async {
    if (_disposed) return;
    // An already completed native check in this process is sufficient.
    if (_verifiedUnsupported != null && snapshot != null) {
      _startupTrusted = true;
      _startupNetworkChecked = true;
      return;
    }
    if (busy) {
      return; // No implicit allow while another operation owns the helper.
    }
    final generation = _lifecycleGeneration;
    bool current() => !_disposed && generation == _lifecycleGeneration;
    _operation = 'startup_policy';
    _changed();
    try {
      try {
        final policy = await _bounded(
          client.readStartupPolicy,
          startupPolicyTimeout,
        );
        if (!current()) return;
        _startupTrusted = true;
        if (policy.state == UpdateStartupPolicyState.verified) {
          _verifiedUnsupported = policy.unsupported;
          _minimumVersion = policy.minimumVersion;
          _changed();
          // An expired manifest can deny a new connection, never produce an
          // install candidate. Refresh in the background once startup is gated.
          if (policy.unsupported) return;
        }
      } on UpdateException catch (error) {
        if (!current()) return;
        errorCode = error.code;
        if (error.code == 'UPDATE_UNCONFIGURED') {
          _startupTrusted = true;
          _startupNetworkChecked = true;
          return;
        }
      } catch (_) {
        if (!current()) return;
        errorCode = 'UPDATE_FAILED';
      }
      if (!current()) return;
      _operation = 'startup_check';
      _startupNetworkChecked = true;
      _changed();
      try {
        final result = await _bounded(client.check, startupCheckTimeout);
        if (!current()) return;
        _acceptSnapshot(result);
        _startupTrusted = true;
        errorCode = null;
      } on UpdateException catch (error) {
        if (current()) errorCode = error.code;
      } catch (_) {
        if (current()) errorCode = 'UPDATE_FAILED';
      }
    } finally {
      _operation = null;
      _changed();
    }
  }

  Future<T> _bounded<T>(Future<T> Function() call, Duration limit) async {
    final pending = call();
    return pending.timeout(
      limit,
      onTimeout: () async {
        client.cancel();
        // Give the exact helper time to retire, but never await an uncooperative
        // transport indefinitely or adopt a result arriving after the deadline.
        await pending
            .then<void>((_) {}, onError: (Object _) {})
            .timeout(const Duration(seconds: 2), onTimeout: () {});
        throw const UpdateException('UPDATE_TIMEOUT');
      },
    );
  }

  /// AppController invokes this immediately before its two automatic paths.
  /// Exceptions skip those paths while keeping account/network recovery usable.
  Future<void> requireStartupDecision() async {
    await prepareStartup();
    if (_disposed || !_startupTrusted) {
      throw const UpdateException('UPDATE_STARTUP_UNCONFIRMED');
    }
    if (_verifiedUnsupported == true) {
      throw const UpdateException('UPDATE_REQUIRED');
    }
  }

  void _acceptSnapshot(UpdateSnapshot value) {
    snapshot = value;
    _verifiedUnsupported = value.unsupported;
    _minimumVersion = value.minimumVersion;
    _startupTrusted = true;
  }

  void _scheduleCheck() {
    _checkTimer?.cancel();
    if (!_background || _disposed) return;
    _checkTimer = _schedule(checkInterval, () {
      _scheduleCheck();
      if (canCheck) unawaited(check());
    });
  }

  void stopBackground() {
    _lifecycleGeneration++;
    _background = false;
    _checkTimer?.cancel();
    _retryTimer?.cancel();
    if (_operation == 'startup_policy' || _operation == 'startup_check') {
      // A partially read policy is not a completed startup decision. A later
      // verified check can recover trust; a cancelled startup cannot allow it.
      _startupTrusted = false;
    }
    // This is lifecycle suspension, not a user cancellation preference.
    if (_operation == 'check' ||
        downloading ||
        _operation == 'startup_policy' ||
        _operation == 'startup_check') {
      client.cancel();
    }
  }

  void _scheduleRetry() {
    _retryTimer?.cancel();
    if (!_background ||
        _disposed ||
        userPaused ||
        _pauseLoadFailed ||
        _retries >= retryDelays.length ||
        !const {
          'UPDATE_WAITING_NETWORK',
          'UPDATE_TIMEOUT',
          'UPDATE_CHECK_FAILED',
          'UPDATE_DOWNLOAD_FAILED',
          'UPDATE_FAILED',
        }.contains(errorCode)) {
      return;
    }
    _retryTimer = _schedule(retryDelays[_retries++], () {
      if (_disposed || !_background || busy || userPaused) return;
      if (canDownload) {
        unawaited(_downloadAutomatically());
      } else {
        unawaited(_check(resetRetries: false));
      }
    });
  }

  Future<void> check() => _check(resetRetries: true);

  Future<void> _check({required bool resetRetries}) async {
    if (!canCheck) return;
    final generation = _lifecycleGeneration;
    await _loadPause();
    if (!canCheck || generation != _lifecycleGeneration) return;
    _retryTimer?.cancel();
    if (resetRetries) _retries = 0;
    // A failed new authority check must not leave an old install action enabled.
    snapshot = null;
    await _run('check', client.check);
    if (_disposed || generation != _lifecycleGeneration) return;
    if (_pauseLoadFailed && errorCode == null) {
      errorCode = 'UPDATE_PREFERENCE_FAILED';
      _changed();
    }
    if (errorCode == null && canDownload) {
      await _downloadAutomatically();
    } else {
      _scheduleRetry();
    }
  }

  Future<void> _downloadAutomatically() async {
    if (!canDownload || userPaused || _pauseLoadFailed) return;
    await _run(
      'download',
      () => client.download(snapshot!.candidateId!, allowMetered: false),
    );
    _scheduleRetry();
  }

  Future<void> download({bool allowMetered = false}) async {
    if (!canDownload) return;
    await _loadPause();
    if (!canDownload) return;
    _retryTimer?.cancel();
    _retries = 0;
    final id = snapshot!.candidateId!;
    _pausedCandidate = null;
    if (!await _savePause(null) || !canDownload) return;
    _pauseLoadFailed = false;
    await _run(
      'download',
      () => client.download(id, allowMetered: allowMetered),
    );
    _scheduleRetry();
  }

  Future<void> cancelDownload() async {
    if (_disposed || (!downloading && !canDownload)) return;
    _retryTimer?.cancel();
    _pausedCandidate = snapshot!.candidateId;
    if (downloading) client.cancel();
    errorCode = 'UPDATE_CANCELLED';
    _changed();
    await _savePause(_pausedCandidate);
  }

  Future<void> install({required bool confirmed}) async {
    if (!confirmed || !canInstall) return;
    final id = snapshot!.candidateId!;
    await _run('install', () async {
      bool paused;
      try {
        paused = await pauseBeforeInstall();
      } catch (_) {
        throw const UpdateException('UPDATE_PAUSE_UNCONFIRMED');
      }
      if (!paused) {
        throw const UpdateException('UPDATE_PAUSE_UNCONFIRMED');
      }
      // Disposal during a pending pause must not launch a new external process.
      if (_disposed) throw const UpdateException('UPDATE_CANCELLED');
      final launched = await client.launch(id, installConsent: true);
      if (launched.state != UpdateState.launchRequested) {
        throw const UpdateException('UPDATE_LAUNCH_OUTCOME_UNKNOWN');
      }
      if (_disposed) return launched;
      snapshot = launched;
      try {
        if (!await handoffAfterLaunch()) {
          throw const UpdateException('UPDATE_HANDOFF_UNCONFIRMED');
        }
      } catch (_) {
        throw const UpdateException('UPDATE_HANDOFF_UNCONFIRMED');
      }
      if (!_disposed) {
        try {
          await exitAfterHandoff();
        } catch (_) {
          throw const UpdateException('UPDATE_EXIT_FAILED');
        }
      }
      return launched;
    });
  }

  Future<void> _run(
    String operation,
    Future<UpdateSnapshot> Function() call,
  ) async {
    _operation = operation;
    errorCode = null;
    _changed();
    try {
      final result = await call();
      if (!_disposed) _acceptSnapshot(result);
    } on UpdateException catch (error) {
      if (!_disposed) {
        errorCode = error.code;
        _launchUncertain = error.code == 'UPDATE_LAUNCH_OUTCOME_UNKNOWN';
      }
    } catch (_) {
      if (!_disposed) {
        _launchUncertain = operation == 'install';
        errorCode = _launchUncertain
            ? 'UPDATE_LAUNCH_OUTCOME_UNKNOWN'
            : 'UPDATE_FAILED';
      }
    } finally {
      _operation = null;
      _changed();
    }
  }

  String get statusText {
    if (_operation == 'startup_policy' || _operation == 'startup_check') {
      return '正在核验启动版本要求，完成前不会自动连接或恢复连接。';
    }
    if (installing) return '正在暂停转发并准备安装；安装前需确认网络保护状态。';
    if (downloading) return '正在下载更新，可以取消并稍后手动继续。';
    if (_operation == 'check') return '正在检查更新…';
    if (snapshot == null && _verifiedUnsupported == true) {
      return '当前版本低于已验证的最低版本 $_minimumVersion，已阻止新连接。'
          '请检查并安装更新；已有保护保持不变。';
    }
    if (_startup != null && !_startupTrusted) {
      return '启动版本策略无法核验，自动连接与更新后恢复已暂停。请重新检查更新或修复安装。';
    }
    if (userPaused && errorCode != 'UPDATE_PREFERENCE_FAILED') {
      return '此版本下载已由你暂停；网络恢复或重启不会自动继续，可手动恢复。';
    }
    if (errorCode != null) {
      return switch (errorCode) {
        'UPDATE_UNCONFIGURED' => '更新通道未配置，当前无法检查或下载更新。',
        'UPDATE_WAITING_NETWORK' => '网络按流量计费或费用未知，下载已等待；可明确允许本次下载。',
        'UPDATE_CANCELLED' => '下载已取消，可手动继续；已下载的数据由更新组件保留。',
        'UPDATE_PAUSE_UNCONFIRMED' => '未能确认转发已暂停，安装未启动；请先检查连接与保护状态。',
        'UPDATE_LAUNCH_CANCELLED' => '已取消 Windows 安装授权，尚未安装。连接保持暂停，可手动重新验证连接。',
        'UPDATE_LAUNCH_OUTCOME_UNKNOWN' =>
          '无法确认安装器是否已启动，请检查 Windows 安装窗口；不会自动重试。连接保持暂停。',
        'UPDATE_HANDOFF_UNCONFIRMED' =>
          '安装器已请求启动，但连接交接未确认，客户端保持打开。请检查保护状态与 Windows 安装窗口，不要重复启动安装。',
        'UPDATE_EXIT_FAILED' => '安装器已请求启动，保护交接已确认，但客户端未能退出。尚未确认安装完成。',
        'UPDATE_TRUST_FAILED' ||
        'UPDATE_PACKAGE_INVALID' ||
        'UPDATE_RELEASE_INVALID' ||
        'UPDATE_PROTOCOL_ERROR' => '更新验证未通过，操作已停止。请使用官方发布的完整安装包。',
        'UPDATE_HELPER_FAILED' ||
        'UPDATE_PLATFORM_UNSUPPORTED' => '更新组件不可用，请使用官方发布的 Windows 安装版。',
        'UPDATE_TIMEOUT' => '更新操作超时，可手动重试。',
        'UPDATE_PREFERENCE_FAILED' => '下载暂停设置无法保存或读取，自动下载已停止；请检查本地存储。',
        _ => '更新操作未完成，可检查网络或稍后重试。',
      };
    }
    return switch (snapshot?.state) {
      UpdateState.upToDate => '当前已是此更新通道的最新版本。',
      UpdateState.available => '发现新版本；符合网络条件时自动后台下载。',
      UpdateState.downloading => '下载未完成，可手动继续。',
      UpdateState.ready => '更新已验证并准备好，等待你确认安装。',
      UpdateState.launchRequested => '已请求启动安装器，尚未确认安装完成。连接保持暂停。',
      null => '尚未检查更新；符合条件的更新会后台下载，安装始终需要你确认。',
    };
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    if (_disposed) return;
    stopBackground();
    _disposed = true;
    unawaited(_progress.cancel());
    // The owner disposes its own client; injected clients belong to the caller.
    super.dispose();
  }
}

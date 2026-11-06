import 'dart:async';

import 'package:flutter/foundation.dart';

import 'crash_reports.dart';

/// Preserves existing framework handling. Error objects and raw stacks never
/// cross into the report coordinator; only a fixed symbol/code is constructed.
class CrashErrorHooks {
  CrashErrorHooks(this.reports);
  final CrashReportCoordinator reports;
  FlutterExceptionHandler? _oldFlutter;
  bool Function(Object, StackTrace)? _oldPlatform;
  bool _attached = false;
  late final FlutterExceptionHandler _flutter = _onFlutter;
  late final bool Function(Object, StackTrace) _platform = _onPlatform;
  void _onFlutter(FlutterErrorDetails details) {
    unawaited(
      reports.record(
        const CrashSignal(
          module: CrashModule.ui,
          code: CrashCode.uiUnhandled,
          frames: [CrashFrame.uiDispatch],
        ),
      ),
    );
    _oldFlutter?.call(details);
  }

  bool _onPlatform(Object error, StackTrace stack) {
    unawaited(
      reports.record(
        const CrashSignal(
          module: CrashModule.ui,
          code: CrashCode.uiUnhandled,
          frames: [CrashFrame.platformDispatch],
        ),
      ),
    );
    return _oldPlatform?.call(error, stack) ?? false;
  }

  void attach() {
    if (_attached) return;
    _attached = true;
    _oldFlutter = FlutterError.onError;
    _oldPlatform = PlatformDispatcher.instance.onError;
    FlutterError.onError = _flutter;
    PlatformDispatcher.instance.onError = _platform;
  }

  void dispose() {
    if (!_attached) return;
    if (identical(FlutterError.onError, _flutter)) {
      FlutterError.onError = _oldFlutter;
    }
    if (identical(PlatformDispatcher.instance.onError, _platform)) {
      PlatformDispatcher.instance.onError = _oldPlatform;
    }
    _attached = false;
  }
}

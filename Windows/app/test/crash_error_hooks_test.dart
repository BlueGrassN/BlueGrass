import 'package:bluegrass_client/crash_error_hooks.dart';
import 'package:bluegrass_client/crash_reports.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

import 'crash_reports_test.dart' show MemoryCrashStore;

void main() {
  test('error hooks keep prior handler behavior but queue only fixed crash signals', () async {
    final reports = CrashReportCoordinator(store: MemoryCrashStore());
    await reports.initialize();
    final original = FlutterError.onError;
    final originalPlatform = PlatformDispatcher.instance.onError;
    var forwarded = 0;
    void previous(FlutterErrorDetails details) {
      forwarded++;
    }

    FlutterError.onError = previous;
    final hooks = CrashErrorHooks(reports)..attach();
    try {
      FlutterError.onError!(
        FlutterErrorDetails(exception: StateError('controlled test failure')),
      );
      await Future<void>.delayed(Duration.zero);
      expect(reports.count, 0);
      await reports.setEnabled(true);
      FlutterError.onError!(
        FlutterErrorDetails(exception: StateError('controlled test failure')),
      );
      await Future<void>.delayed(Duration.zero);
      expect(reports.count, 1);
      expect(forwarded, 2);
      final exported = await reports.exportJson();
      expect(exported, contains('UI_UNHANDLED'));
      expect(exported, isNot(contains('controlled test failure')));
      hooks.dispose();
      expect(FlutterError.onError, same(previous));
      expect(PlatformDispatcher.instance.onError, same(originalPlatform));
    } finally {
      hooks.dispose();
      reports.dispose();
      FlutterError.onError = original;
      PlatformDispatcher.instance.onError = originalPlatform;
    }
  });
}

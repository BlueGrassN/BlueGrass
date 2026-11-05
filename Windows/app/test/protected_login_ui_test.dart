import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

class ProtectedController extends AppController {
  ProtectedController({required this.permitLogin, this.permitRetry = false})
    : super(source: FixtureDataSource()) {
    connection = 'paused';
    actualMode = 'tun';
  }
  final bool permitLogin, permitRetry;
  int loginCalls = 0, retryCalls = 0, disconnectCalls = 0;
  @override
  bool get active => true;
  @override
  bool get canLogin => permitLogin;
  @override
  bool get canRetryProtected => permitRetry;
  @override
  Future<void> initialize({
    Future<void> Function()? beforeAutomaticConnect,
  }) async {
    initializing = false;
    storageReady = true;
    notifyListeners();
  }

  @override
  Future<void> login(String username, String password) async => loginCalls++;
  @override
  Future<void> retryProtectedConnection() async => retryCalls++;
  @override
  Future<bool> disconnect() async {
    disconnectCalls++;
    return true;
  }
}

void main() {
  testWidgets(
    'known protected pause permits login without releasing protection',
    (tester) async {
      final c = ProtectedController(permitLogin: true);
      try {
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        await tester.enterText(find.byType(TextField).first, 'fixture');
        await tester.enterText(find.byType(TextField).last, 'fixture');
        final login = find.widgetWithText(FilledButton, '登录');
        expect(tester.widget<FilledButton>(login).onPressed, isNotNull);
        await tester.ensureVisible(login);
        await tester.tap(login);
        await tester.pumpAndSettle();
        expect(c.loginCalls, 1);
        expect(c.disconnectCalls, 0);
        expect(find.text('重新验证并连接'), findsNothing);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    },
  );
  testWidgets('unknown startup protection stays recovery-only', (tester) async {
    final c = ProtectedController(permitLogin: false);
    try {
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<FilledButton>(find.widgetWithText(FilledButton, '登录'))
            .onPressed,
        isNull,
      );
      expect(find.text('重新验证并连接'), findsNothing);
      expect(find.text('恢复网络并断开'), findsOneWidget);
    } finally {
      await tester.pumpWidget(const SizedBox());
      c.dispose();
    }
  });
  testWidgets('explicit protected retry invokes reproof without disconnect', (
    tester,
  ) async {
    final c = ProtectedController(permitLogin: true, permitRetry: true);
    try {
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      await tester.tap(find.text('重新验证并连接'));
      await tester.pumpAndSettle();
      expect(c.retryCalls, 1);
      expect(c.disconnectCalls, 0);
      expect(find.textContaining('保留网络保护'), findsOneWidget);
    } finally {
      await tester.pumpWidget(const SizedBox());
      c.dispose();
    }
  });
}

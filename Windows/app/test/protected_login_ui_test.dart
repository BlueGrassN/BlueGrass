import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
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
  Future<void> clickAndTypeCredentials(WidgetTester tester) async {
    final fields = find.byType(TextField);
    for (final (index, text) in [
      (0, 'fixture-account'),
      (1, 'fixture-password'),
    ]) {
      final field = fields.at(index);
      await tester.ensureVisible(field);
      await tester.tap(field, kind: PointerDeviceKind.mouse);
      await tester.pump();
      final editable = tester.widget<EditableText>(
        find.descendant(of: field, matching: find.byType(EditableText)),
      );
      expect(
        editable.focusNode.hasFocus,
        isTrue,
        reason: 'Clicking the field must acquire focus without forcing it',
      );
      tester.testTextInput.enterText(text);
      await tester.pump();
      expect(tester.widget<TextField>(field).controller!.text, text);
    }
    expect(tester.widget<TextField>(fields.last).obscureText, isTrue);
  }

  for (final target in [TargetPlatform.macOS, TargetPlatform.windows]) {
    testWidgets('$target accepts credential input while startup is pending', (
      tester,
    ) async {
      debugDefaultTargetPlatformOverride = target;
      tester.view.physicalSize = const Size(1040, 900);
      tester.view.devicePixelRatio = 1;
      final deviceReady = Completer<String>();
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () => deviceReady.future,
        readPreferences: () async => const Preferences(),
        nativeFactory: () => throw StateError('startup is still waiting'),
      );
      try {
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        expect(c.initializing, isTrue);
        expect(c.canLogin, isFalse);
        expect(c.message, isNull);
        await clickAndTypeCredentials(tester);
        expect(
          tester
              .widget<FilledButton>(find.widgetWithText(FilledButton, '正在准备'))
              .onPressed,
          isNull,
        );
        await tester.testTextInput.receiveAction(TextInputAction.done);
        await tester.pump();
        expect(
          c.customer,
          isNull,
          reason: 'Typing must not bypass startup checks',
        );
      } finally {
        deviceReady.completeError(StateError('end pending storage fixture'));
        await tester.pump();
        await tester.pumpWidget(const SizedBox());
        c.dispose();
        tester.view.resetPhysicalSize();
        tester.view.resetDevicePixelRatio();
        debugDefaultTargetPlatformOverride = null;
      }
    });
  }

  testWidgets(
    'known protected pause permits login without releasing protection',
    (tester) async {
      final c = ProtectedController(permitLogin: true);
      try {
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        await clickAndTypeCredentials(tester);
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
      await clickAndTypeCredentials(tester);
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pump();
      expect(c.loginCalls, 0);
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
  testWidgets('login in flight still locks credential editing', (tester) async {
    final c = ProtectedController(permitLogin: true)..busy = true;
    try {
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      for (final field in find.byType(TextField).evaluate()) {
        final finder = find.byWidget(field.widget);
        await tester.ensureVisible(finder);
        await tester.tap(finder, kind: PointerDeviceKind.mouse);
        await tester.pump();
        final editable = tester.widget<EditableText>(
          find.descendant(of: finder, matching: find.byType(EditableText)),
        );
        expect(editable.focusNode.hasFocus, isFalse);
      }
      expect(c.loginCalls, 0);
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

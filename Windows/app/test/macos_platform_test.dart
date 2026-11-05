import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/app_theme.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/diagnostic_client.dart';
import 'package:bluegrass_client/crash_reports.dart';
import 'package:bluegrass_client/crash_report_settings.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_settings.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'update_startup_policy_test.dart' show StartupClient;
import 'crash_reports_test.dart' show MemoryCrashStore, signal;
import 'api_endpoint_test.dart' show ConfigTransport, config, jsonResponse;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => debugDefaultTargetPlatformOverride = TargetPlatform.macOS);
  tearDown(() => debugDefaultTargetPlatformOverride = null);

  for (final (target, seconds) in [
    (TargetPlatform.macOS, 90),
    (TargetPlatform.windows, 45),
  ]) {
    test('$target native request default is ${seconds}s with 5s shutdown', () {
      debugDefaultTargetPlatformOverride = target;
      final native = NativeClient();
      expect(native.requestTimeout, Duration(seconds: seconds));
      expect(native.shutdownTimeout, const Duration(seconds: 5));
    });

    test('$target preserves explicit native timeout overrides', () {
      debugDefaultTargetPlatformOverride = target;
      final requestOnly = NativeClient(
        requestTimeout: const Duration(milliseconds: 125),
      );
      expect(requestOnly.requestTimeout, const Duration(milliseconds: 125));
      expect(requestOnly.shutdownTimeout, const Duration(seconds: 5));
      final both = NativeClient(
        requestTimeout: const Duration(milliseconds: 250),
        shutdownTimeout: const Duration(milliseconds: 500),
      );
      expect(both.requestTimeout, const Duration(milliseconds: 250));
      expect(both.shutdownTimeout, const Duration(milliseconds: 500));
      final shutdownOnly = NativeClient(
        shutdownTimeout: const Duration(milliseconds: 750),
      );
      expect(shutdownOnly.requestTimeout, Duration(seconds: seconds));
      expect(shutdownOnly.shutdownTimeout, const Duration(milliseconds: 750));
    });

    test('$target validates resolved native timeout fields', () {
      debugDefaultTargetPlatformOverride = target;
      for (final invalid in [Duration.zero, const Duration(milliseconds: -1)]) {
        expect(
          () => NativeClient(requestTimeout: invalid),
          throwsArgumentError,
        );
        expect(
          () => NativeClient(shutdownTimeout: invalid),
          throwsArgumentError,
        );
        expect(
          () => NativeClient(
            requestTimeout: const Duration(milliseconds: 125),
            shutdownTimeout: invalid,
          ),
          throwsArgumentError,
        );
      }
    });
  }

  test('Mac production bootstraps and logs in without native control HTTP', () async {
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    final calls = <String>[];
    final records = <String, String>{
      'device_id': 'b5e5d810-8032-4e02-9f05-a756531f4b88',
    };
    messenger.setMockMethodCallHandler(platform, (call) async {
      calls.add(call.method);
      if (call.method.startsWith('controlHttp')) {
        throw PlatformException(code: 'unsupported');
      }
      final args = call.arguments as Map?;
      if (call.method == 'readRecord') return records[args!['key']];
      if (call.method == 'writeRecord') records[args!['key']] = args['value'];
      if (call.method == 'deleteRecord') records.remove(args!['key']);
      return null;
    });
    const next = 'https://api-next.sunip.vip/api/client/v1';
    final bootstrap = ConfigTransport(jsonResponse(config(2, next)));
    final api = ConfigTransport(
      jsonResponse({
        'error': {'code': 'invalid_credentials'},
      }, status: 401),
    );
    final c = AppController.production(
      unprotectedTransport: api,
      bootstrapTransport: bootstrap,
      nativeFactory: () => NativeClient(
        startProcess: (exe, args) {
          expect(exe, endsWith('BlueGrass.Native'));
          expect(args, ['--system-proxy']);
          final dart = File(Platform.resolvedExecutable).parent.uri
              .resolve(
                '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
              )
              .toFilePath();
          return Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            'valid',
          ]);
        },
      ),
    );
    try {
      await c.initialize();
      expect(c.storageReady, isTrue);
      expect((c.source as ApiV2DataSource).environment, next);
      expect(
        bootstrap.requests.single.uri.toString(),
        'https://client-api.sunip.vip/client-config/config.json',
      );
      await c.login('account', 'password');
      expect(api.requests, hasLength(1), reason: c.message);
      expect(api.requests.single.uri.toString(), '$next/auth/login');
      expect(api.requests.single.body!['device_name'], 'BlueGrass macOS');
      expect(
        c.customer,
        isNull,
        reason: 'the real API denial must not become a fixture login',
      );
      expect(calls.where((name) => name.startsWith('controlHttp')), isEmpty);
    } finally {
      c.dispose();
      messenger.setMockMethodCallHandler(platform, null);
      debugDefaultTargetPlatformOverride = null;
    }
  });

  for (final path in <String?>[
    '/Users/test/Desktop/crash-reports.json',
    null,
  ]) {
    testWidgets(
      'Mac crash export uses save dialog and handles ${path == null ? 'cancellation' : 'success'}',
      (tester) async {
        final calls = <MethodCall>[];
        tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          platform,
          (call) async {
            calls.add(call);
            return path;
          },
        );
        final reports = CrashReportCoordinator(store: MemoryCrashStore());
        await reports.initialize();
        await reports.setEnabled(true);
        await reports.record(signal);
        try {
          await tester.pumpWidget(
            MaterialApp(
              home: Scaffold(body: CrashReportSettings(reports: reports)),
            ),
          );
          await tester.tap(find.text('预览并导出'));
          await tester.pumpAndSettle();
          await tester.tap(find.text('保存 JSON 文件'));
          await tester.pumpAndSettle();
          expect(calls, hasLength(1));
          expect(calls.single.method, 'saveFile');
          final args = calls.single.arguments as Map;
          expect(args['suggestedName'], 'crash-reports.json');
          expect(jsonDecode(args['content'])['reports'], hasLength(1));
          expect(find.text(path ?? '已取消导出。'), findsOneWidget);
        } finally {
          await tester.pumpWidget(const SizedBox());
          reports.dispose();
          tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
            platform,
            null,
          );
          debugDefaultTargetPlatformOverride = null;
        }
      },
    );
  }

  test(
    'Mac launches adjacent native and diagnostic helpers without exe',
    () async {
      final paths = <String>[];
      final native = NativeClient(
        startProcess: (exe, args) async {
          paths.add(exe);
          throw StateError('no process in path test');
        },
      );
      final diagnostics = DiagnosticClient(
        startProcess: (exe, args) async {
          paths.add(exe);
          throw StateError('no process in path test');
        },
      );
      await expectLater(native.start(), throwsA(isA<NativeClientException>()));
      await expectLater(diagnostics.run({}), throwsStateError);
      expect(paths[0], endsWith('BlueGrass.Native'));
      expect(paths[1], endsWith('BlueGrass.Diagnostics'));
    },
  );

  test('Mac refuses TUN before starting any child', () async {
    var launches = 0;
    final native = NativeClient(
      startProcess: (exe, args) async {
        launches++;
        throw StateError('must not launch');
      },
    );
    await expectLater(
      native.start(mode: 'tun'),
      throwsA(
        isA<NativeClientException>().having(
          (e) => e.code,
          'code',
          'UNSUPPORTED_COMMAND',
        ),
      ),
    );
    expect(launches, 0);
  });

  test(
    'Mac refuses a direct native Connect TUN request before launch',
    () async {
      var launches = 0;
      final native = NativeClient(
        startProcess: (exe, args) async {
          launches++;
          throw StateError('must not launch');
        },
      );
      await expectLater(
        native.request('Connect', mode: 'tun'),
        throwsA(
          isA<NativeClientException>().having(
            (e) => e.code,
            'code',
            'UNSUPPORTED_COMMAND',
          ),
        ),
      );
      expect(launches, 0);
    },
  );

  test(
    'Windows still resolves adjacent exe helpers and accepts TUN startup',
    () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      final paths = <String>[];
      final native = NativeClient(
        startProcess: (exe, args) async {
          expect(args, ['--tun']);
          paths.add(exe);
          throw StateError('path capture');
        },
      );
      final diagnostic = DiagnosticClient(
        startProcess: (exe, args) async {
          paths.add(exe);
          throw StateError('path capture');
        },
      );
      await expectLater(
        native.start(mode: 'tun'),
        throwsA(isA<NativeClientException>()),
      );
      await expectLater(diagnostic.run({}), throwsStateError);
      expect(paths, [
        File(Platform.resolvedExecutable).parent.uri
            .resolve('BlueGrass.Native.exe')
            .toFilePath(),
        File(Platform.resolvedExecutable).parent.uri
            .resolve('BlueGrass.Diagnostics.exe')
            .toFilePath(),
      ]);
    },
  );

  test(
    'Mac rejects every Windows updater operation without spawning',
    () async {
      var launches = 0;
      final client = UpdateClient.withProcessStarter((exe, args) async {
        launches++;
        throw StateError('must not launch');
      });
      final candidate = 'a' * 64;
      for (final operation in [
        client.readStartupPolicy,
        client.check,
        () => client.download(candidate),
        () => client.launch(candidate, installConsent: true),
      ]) {
        await expectLater(
          operation(),
          throwsA(
            isA<UpdateException>().having(
              (e) => e.code,
              'code',
              'UPDATE_PLATFORM_UNSUPPORTED',
            ),
          ),
        );
      }
      expect(launches, 0);
      await client.dispose();
    },
  );

  test(
    'Mac crash summaries keep their platform across protected queue reload',
    () async {
      final store = MemoryCrashStore();
      var reports = CrashReportCoordinator(store: store);
      await reports.initialize();
      await reports.setEnabled(true);
      await reports.record(signal);
      expect(
        jsonDecode(store.value!)['queue'][0]['report']['platform'],
        'macos',
      );
      reports.dispose();
      reports = CrashReportCoordinator(store: store);
      await reports.initialize();
      expect(reports.count, 1);
      reports.dispose();
    },
  );

  test(
    'Mac normalizes stale TUN preferences and refuses programmatic TUN',
    () async {
      final writes = <Preferences>[];
      final c = AppController(
        source: FixtureDataSource(),
        demo: true,
        getDeviceId: () async => 'local',
        readPreferences: () async => const Preferences(mode: 'tun'),
        savePreferences: (value) async => writes.add(value),
      );
      addTearDown(c.dispose);
      await c.initialize();
      expect(c.preferences.mode, 'system_proxy');
      await c.updatePreferences(c.preferences.copyWith(mode: 'tun'));
      expect(c.preferences.mode, 'system_proxy');
      expect(writes.where((p) => p.mode == 'tun'), isEmpty);
      expect(c.message, contains('开发版本暂不支持 TUN'));
      expect(jsonDecode(c.diagnosticJson())['platform'], 'macos');
      await c.login('fixture', 'fixture');
      c.preferences = c.preferences.copyWith(mode: 'tun');
      await c.connect();
      expect(
        c.connected,
        isFalse,
        reason: 'direct preference assignment must not bypass the capability',
      );
    },
  );

  for (final width in [480.0, 1400.0]) {
    for (final brightness in Brightness.values) {
      testWidgets(
        'Mac shared navigation, theme and controls at $width/$brightness',
        (tester) async {
          tester.view.physicalSize = Size(width, 1500);
          tester.view.devicePixelRatio = 1;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          final c = fixtureController();
          await tester.pumpWidget(MyApp(controller: c));
          await tester.pumpAndSettle();
          if (width >= 900) {
            expect(find.text('凭据由 macOS 钥匙串加密保存'), findsOneWidget);
          }
          await c.updatePreferences(
            c.preferences.copyWith(
              theme: brightness == Brightness.dark
                  ? ThemeMode.dark
                  : ThemeMode.light,
            ),
          );
          await tester.pumpAndSettle();
          await c.login('fixture', 'fixture');
          await tester.pumpAndSettle();
          final tun = tester.widget<Switch>(
            find.byKey(const ValueKey('tun-mode-button')),
          );
          expect(tun.onChanged, isNull);
          expect(tun.value, isFalse);
          expect(find.textContaining('开发版本暂不支持 TUN'), findsWidgets);
          for (final title in ['我的 IP', '诊断', '设置', '关于']) {
            await tester.tap(find.text(title).last);
            await tester.pumpAndSettle();
            expect(tester.takeException(), isNull);
            if (title == '设置') {
              expect(find.text('登录 macOS 时启动'), findsOneWidget);
              expect(find.textContaining('菜单栏'), findsWidgets);
              expect(
                tester
                    .widget<SwitchListTile>(
                      find.widgetWithText(SwitchListTile, 'TUN 模式'),
                    )
                    .onChanged,
                isNull,
              );
            }
          }
          expect(find.textContaining('macOS 客户端'), findsOneWidget);
          expect(find.textContaining('手动'), findsWidgets);
          expect(find.textContaining('钥匙串'), findsWidgets);
          final macTheme = blueGrassTheme(brightness);
          debugDefaultTargetPlatformOverride = TargetPlatform.windows;
          final windowsTheme = blueGrassTheme(brightness);
          expect(macTheme.colorScheme, windowsTheme.colorScheme);
          expect(
            macTheme.textTheme.bodyMedium?.fontSize,
            windowsTheme.textTheme.bodyMedium?.fontSize,
          );
          expect(macTheme.textTheme.bodyMedium?.fontFamily, 'PingFang SC');
          expect(
            windowsTheme.textTheme.bodyMedium?.fontFamily,
            'Microsoft YaHei',
          );
          debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
          await tester.pumpWidget(const SizedBox());
          c.dispose();
          debugDefaultTargetPlatformOverride = null;
        },
      );
    }
  }

  testWidgets(
    'Mac ignores even an injected Windows updater on startup and resume',
    (tester) async {
      final client = StartupClient();
      final updates = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => throw StateError('unexpected install'),
        handoffAfterLaunch: () async => throw StateError('unexpected handoff'),
        exitAfterHandoff: () async => throw StateError('unexpected exit'),
      );
      final c = fixtureController();
      await tester.pumpWidget(MyApp(controller: c, updates: updates));
      await tester.pumpAndSettle();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump(const Duration(minutes: 31));
      expect(client.calls, isEmpty);
      expect(find.byType(UpdateReadyBanner), findsNothing);
      await tester.pumpWidget(const SizedBox());
      c.dispose();
      updates.dispose();
      await client.dispose();
      debugDefaultTargetPlatformOverride = null;
    },
  );
}

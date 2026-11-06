import 'package:bluegrass_client/app_theme.dart';
import 'package:bluegrass_client/src/android/app_theme.dart' as android;
import 'package:bluegrass_client/src/presentation/connection_content.dart';
import 'package:bluegrass_client/src/presentation/login_content.dart';
import 'package:bluegrass_client/src/presentation/resource_content.dart';
import 'package:bluegrass_client/src/presentation/routing_content.dart';
import 'package:bluegrass_client/src/presentation/settings_content.dart';
import 'package:bluegrass_client/src/presentation/traffic_content.dart';
import 'package:bluegrass_client/ui/navigation.dart';
import 'package:bluegrass_client/ui/start_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

const destinations = [
  AppDestination(
    label: '首页',
    icon: Icons.home_outlined,
    selectedIcon: Icons.home,
  ),
  AppDestination(label: '资源', icon: Icons.public, selectedIcon: Icons.public),
  AppDestination(
    label: '设置',
    icon: Icons.settings_outlined,
    selectedIcon: Icons.settings,
  ),
];

void main() {
  for (final brightness in Brightness.values) {
    for (final width in [320.0, 390.0, 1280.0]) {
      testWidgets(
        'shared features $width $brightness preserve gates and actions',
        (tester) async {
          tester.view.physicalSize = Size(width, 1000);
          tester.view.devicePixelRatio = 1;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          final theme = blueGrassTheme(brightness);
          expect(
            android.blueGrassTheme(brightness).colorScheme,
            theme.colorScheme,
          );
          var selected = 0, details = 0, runs = 0, copies = 0, connected = 0;
          String? routing;
          ThemeMode? mode;
          Widget screen(Widget content) => MaterialApp(
            theme: theme,
            home: Scaffold(
              body: SingleChildScrollView(
                padding: const EdgeInsets.all(16),
                child: content,
              ),
            ),
          );
          await tester.pumpWidget(
            screen(
              Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  ResourceContent(
                    data: const ResourceViewData(
                      name: '香港节点',
                      metadata: 'SOCKS5 · 中国香港',
                      remark: '办公线路',
                      selected: false,
                    ),
                    onSelect: () => selected++,
                    onDetails: () => details++,
                  ),
                  ResourceContent(
                    data: const ResourceViewData(
                      name: '过期节点',
                      metadata: 'SOCKS5',
                      selected: false,
                      unavailable: '已过期',
                    ),
                    onSelect: null,
                    onDetails: () => details++,
                  ),
                  const ConnectionContent(data: ConnectionViewData()),
                  const TrafficMetricContent(
                    data: TrafficMetricData(
                      label: '上传速度',
                      value: '—',
                      unit: '',
                    ),
                  ),
                  RoutingModeControl(
                    value: 'bypass_cn',
                    onChanged: (value) => routing = value,
                  ),
                  ThemeModeControl(
                    value: ThemeMode.system,
                    onChanged: (value) => mode = value,
                  ),
                  DiagnosticsActions(
                    description: '检查当前连接',
                    checking: false,
                    onRun: () => runs++,
                    onCopy: () => copies++,
                  ),
                ],
              ),
            ),
          );
          await tester.pumpAndSettle();
          await tester.tap(find.text('香港节点'));
          expect(selected, 1);
          await tester.tap(find.text('过期节点'));
          expect(
            selected,
            1,
            reason: 'an expired resource never becomes selectable',
          );
          final beforeDetails = details;
          await tester.tap(find.byTooltip('资源详情').last);
          expect(details, beforeDetails + 1);
          expect(find.text('暂未获取'), findsOneWidget);
          expect(find.text('—'), findsOneWidget);
          expect(find.text('0'), findsNothing);
          Future<void> reveal(Finder f) async {
            await tester.ensureVisible(f);
            await tester.pumpAndSettle();
            await tester.tap(f);
            await tester.pumpAndSettle();
          }

          await reveal(find.text('全局代理'));
          expect(routing, 'global');
          await reveal(find.byKey(const Key('theme-dark')));
          expect(mode, ThemeMode.dark);
          await reveal(find.byKey(const Key('run-diagnostics')));
          await reveal(find.byKey(const Key('copy-diagnostics')));
          expect((runs, copies), (1, 1));
          expect(tester.takeException(), isNull);

          await tester.pumpWidget(
            screen(
              Column(
                children: [
                  const RoutingModeControl(value: 'bypass_cn', onChanged: null),
                  const DiagnosticsActions(
                    description: '检查当前连接',
                    checking: true,
                    onRun: null,
                    onCopy: null,
                  ),
                  StartToggleButton(
                    running: false,
                    onPressed: () => connected++,
                    icon: Icons.play_arrow,
                    label: '连接',
                    buttonKey: const Key('power'),
                  ),
                ],
              ),
            ),
          );
          await tester.pumpAndSettle();
          expect(
            tester
                .widget<SegmentedButton<String>>(
                  find.byType(SegmentedButton<String>),
                )
                .onSelectionChanged,
            isNull,
          );
          expect(
            tester
                .widget<FilledButton>(find.byKey(const Key('run-diagnostics')))
                .onPressed,
            isNull,
          );
          expect(
            tester
                .widget<OutlinedButton>(
                  find.byKey(const Key('copy-diagnostics')),
                )
                .onPressed,
            isNull,
          );
          await tester.tap(find.byKey(const Key('power')));
          expect(connected, 1);
          expect(tester.takeException(), isNull);
        },
      );

      testWidgets(
        'login and navigation $width $brightness retain local state',
        (tester) async {
          tester.view.physicalSize = Size(width, 1000);
          tester.view.devicePixelRatio = 1;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          final user = TextEditingController(),
              password = TextEditingController();
          addTearDown(user.dispose);
          addTearDown(password.dispose);
          var submitted = 0, websites = 0;
          final form = GlobalKey<FormState>();
          Widget login(bool allowed, Brightness brightness) => MaterialApp(
            theme: blueGrassTheme(brightness),
            home: Scaffold(
              body: SingleChildScrollView(
                padding: const EdgeInsets.all(16),
                child: Form(
                  key: form,
                  child: LoginContent(
                    data: LoginViewData(
                      fieldsEnabled: true,
                      submitLabel: '登录',
                      onSubmit: !allowed
                          ? null
                          : () {
                              if (form.currentState!.validate()) submitted++;
                            },
                      onWebsite: () => websites++,
                    ),
                    username: user,
                    password: password,
                    obscure: true,
                    onTogglePassword: () {},
                    validate: true,
                    brand: const Text('BlueGrass'),
                  ),
                ),
              ),
            ),
          );
          await tester.pumpWidget(login(true, brightness));
          await tester.tap(find.byKey(const Key('login')));
          await tester.pumpAndSettle();
          expect(submitted, 0);
          expect(find.text('请输入账号'), findsOneWidget);
          expect(find.text('请输入密码'), findsOneWidget);
          await tester.enterText(
            find.byKey(const Key('username')),
            'saved-account',
          );
          await tester.enterText(
            find.byKey(const Key('password')),
            'local-password',
          );
          await tester.pumpWidget(
            login(
              false,
              brightness == Brightness.dark
                  ? Brightness.light
                  : Brightness.dark,
            ),
          );
          expect(user.text, 'saved-account');
          expect(password.text, 'local-password');
          expect(
            tester
                .widget<FilledButton>(find.byKey(const Key('login')))
                .onPressed,
            isNull,
          );
          await tester.testTextInput.receiveAction(TextInputAction.done);
          expect(
            submitted,
            0,
            reason: 'keyboard submission must obey the protection gate',
          );
          await tester.tap(find.text('注册账号'));
          await tester.tap(find.text('找回密码'));
          expect(websites, 2);
          await tester.pumpWidget(login(true, brightness));
          await tester.tap(find.byKey(const Key('login')));
          await tester.pumpAndSettle();
          expect(submitted, 1);

          var page = 0;
          await tester.pumpWidget(
            MaterialApp(
              theme: blueGrassTheme(brightness),
              home: Scaffold(
                body: StatefulBuilder(
                  builder: (context, setState) => ClientNavigationFrame(
                    destinations: destinations,
                    selectedIndex: page,
                    onSelected: (v) => setState(() => page = v),
                    child: Text('page-$page'),
                  ),
                ),
              ),
            ),
          );
          await tester.pumpAndSettle();
          expect(
            find.byType(AppDock),
            width < 840 ? findsOneWidget : findsNothing,
          );
          expect(
            find.byType(AppSidebar),
            width >= 840 ? findsOneWidget : findsNothing,
          );
          await tester.tap(find.text('资源'));
          await tester.pumpAndSettle();
          expect(find.text('page-1'), findsOneWidget);
          tester.view.physicalSize = Size(width < 840 ? 1280 : 390, 1000);
          await tester.pumpAndSettle();
          expect(find.text('page-1'), findsOneWidget);
          await tester.tap(find.text('设置'));
          await tester.pumpAndSettle();
          expect(find.text('page-2'), findsOneWidget);
          expect(tester.takeException(), isNull);
        },
      );
    }
  }
}

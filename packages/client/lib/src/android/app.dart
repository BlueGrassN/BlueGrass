import 'dart:async';

import 'package:flutter/material.dart';

import '../../ui/navigation.dart';
import 'brand.dart';
import 'app_controller.dart';
import 'app_theme.dart';
import 'screens/navigation_dock.dart';
import 'screens/diagnostics_screen.dart';
import 'screens/login_screen.dart';
import 'screens/home_screen.dart';
import 'screens/resources_screen.dart';
import 'screens/settings_screen.dart';

class BlueGrassApp extends StatefulWidget {
  const BlueGrassApp({super.key, required this.controller});
  final AppController controller;
  @override
  State<BlueGrassApp> createState() => _BlueGrassAppState();
}

class _BlueGrassAppState extends State<BlueGrassApp>
    with WidgetsBindingObserver {
  AppController get controller => widget.controller;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(controller.resumeVpn());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: controller,
    builder: (context, _) => MaterialApp(
      title: 'BlueGrass',
      debugShowCheckedModeBanner: false,
      theme: blueGrassTheme(Brightness.light),
      darkTheme: blueGrassTheme(Brightness.dark),
      themeMode: controller.themeMode,
      home: !controller.initialized
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : controller.customer == null
          ? LoginScreen(controller: controller)
          : _Shell(
              key: ValueKey(controller.customer!.id),
              controller: controller,
            ),
    ),
  );
}

class _Shell extends StatefulWidget {
  const _Shell({super.key, required this.controller});
  final AppController controller;
  @override
  State<_Shell> createState() => _ShellState();
}

class _ShellState extends State<_Shell> {
  int _index = 0;
  final _pagesKey = GlobalKey();
  void _select(int value) {
    FocusScope.of(context).unfocus();
    setState(() => _index = value);
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: _index == 0,
    onPopInvokedWithResult: (didPop, _) {
      if (!didPop && _index != 0) _select(0);
    },
    child: LayoutBuilder(
      builder: (context, size) => Scaffold(
        extendBody: false,
        appBar: AppBar(
          title: Text(['仪表盘', '代理', '设置', '诊断'][_index]),
          actions: [
            if (_index < 2)
              IconButton.filledTonal(
                tooltip: '刷新资源',
                onPressed: widget.controller.loading
                    ? null
                    : widget.controller.refresh,
                icon: const Icon(Icons.refresh),
              ),
            const SizedBox(width: 16),
          ],
        ),
        body: ClientNavigationFrame(
          destinations: androidDestinations,
          selectedIndex: _index,
          onSelected: _select,
          brand: const BlueGrassBrand(size: 19),
          scaffoldOwnsDock: true,
          child: SafeArea(
            key: _pagesKey,
            top: false,
            bottom: false,
            child: IndexedStack(
              index: _index,
              children: [
                HomeScreen(
                  controller: widget.controller,
                  active: _index == 0,
                  chooseResource: () => _select(1),
                  openDiagnostics: () => _select(3),
                ),
                ResourcesScreen(controller: widget.controller),
                SettingsScreen(
                  controller: widget.controller,
                  active: _index == 2,
                ),
                DiagnosticsScreen(controller: widget.controller),
              ],
            ),
          ),
        ),
        bottomNavigationBar: usesNavigationDock(size.maxWidth)
            ? NavigationDock(index: _index, onSelected: _select)
            : null,
      ),
    ),
  );
}

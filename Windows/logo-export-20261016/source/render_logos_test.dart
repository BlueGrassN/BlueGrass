// Rendering utility for exporting the existing logo; not an application test.
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'app_theme.dart';
import 'extracted_brand.dart';

const output = String.fromEnvironment('LOGO_EXPORT');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    if (output.isEmpty) throw StateError('LOGO_EXPORT is required');
    final text = FontLoader('Microsoft YaHei');
    for (final path in ['C:/Windows/Fonts/msyh.ttc', 'C:/Windows/Fonts/msyhbd.ttc']) {
      text.addFont(File(path).readAsBytes().then(ByteData.sublistView));
    }
    await text.load();
    final icons = FontLoader('MaterialIcons')
      ..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
    await icons.load();
  });

  testWidgets('export existing Windows logo variants', (tester) async {
    tester.view.physicalSize = const Size(1200, 900);
    tester.view.devicePixelRatio = 1;
    final records = <Map<String, Object>>[];

    Future<void> capture(String name, Widget logo, Brightness brightness, List<double> ratios) async {
      final key = GlobalKey();
      final theme = blueGrassTheme(brightness);
      await tester.pumpWidget(
        Directionality(
          textDirection: TextDirection.ltr,
          child: Theme(
            data: theme,
            child: Center(
              child: RepaintBoundary(
                key: key,
                child: Material(type: MaterialType.transparency, child: logo),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final error = tester.takeException();
      if (error != null) throw error;
      final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      for (final ratio in ratios) {
        final filename = '$name@${ratio.toInt()}x.png';
        await tester.runAsync(() async {
          final image = await boundary.toImage(pixelRatio: ratio);
          try {
            final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
            await File('$output/ui/$filename').writeAsBytes(bytes!.buffer.asUint8List());
            records.add({
              'file': 'ui/$filename',
              'width': image.width,
              'height': image.height,
              'logicalWidth': boundary.size.width,
              'logicalHeight': boundary.size.height,
              'pixelRatio': ratio,
            });
          } finally {
            image.dispose();
          }
        });
      }
    }

    for (final brightness in Brightness.values) {
      final variant = brightness == Brightness.dark ? 'dark' : 'light';
      for (final size in [23.0, 25.0, 28.0]) {
        await capture('bluegrass-wordmark-$variant-${size.toInt()}', Padding(padding: const EdgeInsets.all(2), child: ExtractedBrand(size: size)), brightness, [1, 4]);
      }
      await capture(
        'bluegrass-grass-$variant-256',
        Icon(Icons.grass, size: 256, color: blueGrassTheme(brightness).colorScheme.primary),
        brightness,
        [1, 4],
      );
    }
    File('$output/verification/flutter-render.json').writeAsStringSync(const JsonEncoder.withIndent('  ').convert(records));
    expect(records.length, 16);
    await tester.pumpWidget(const SizedBox());
  });
}

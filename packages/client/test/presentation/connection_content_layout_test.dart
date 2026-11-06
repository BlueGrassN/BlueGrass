import 'package:bluegrass_client/app_theme.dart';
import 'package:bluegrass_client/src/presentation/connection_content.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  const ip = '198.198.198.198';
  for (final brightness in Brightness.values) {
    for (final phone in [true, false]) {
      testWidgets(
        'complete exit IP is visible without scrolling: ${phone ? '320dp / 1.8x' : 'desktop'} / $brightness',
        (tester) async {
          tester.view.physicalSize = Size(phone ? 320 : 1280, 800);
          tester.view.devicePixelRatio = 1;
          tester.platformDispatcher.textScaleFactorTestValue = phone ? 1.8 : 1;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
          await tester.pumpWidget(
            MaterialApp(
              theme: blueGrassTheme(brightness),
              home: Scaffold(
                body: Padding(
                  // Phone page (16) plus dashboard card (20) on each side.
                  padding: const EdgeInsets.all(36),
                  child: Align(
                    alignment: Alignment.topLeft,
                    child: SizedBox(
                      width: phone ? 248 : 400,
                      child: ConnectionContent(
                        large: phone,
                        data: const ConnectionViewData(exitIp: ip),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          );
          await tester.pumpAndSettle();
          final editable = tester.state<EditableTextState>(
            find.byType(EditableText),
          );
          final render = editable.renderEditable;
          final viewport = Offset.zero & render.size;
          // Inspect every character before any selection gesture or scrolling.
          // A single-line SelectableText can contain the complete string while
          // painting its suffix outside the clipped horizontal viewport.
          for (var i = 0; i < ip.length; i++) {
            final boxes = render.getBoxesForSelection(
              TextSelection(baseOffset: i, extentOffset: i + 1),
            );
            expect(boxes, isNotEmpty, reason: 'character $i must be laid out');
            for (final box in boxes) {
              final rect = box.toRect();
              expect(
                rect.left,
                greaterThanOrEqualTo(viewport.left - .5),
                reason: 'character $i left',
              );
              expect(
                rect.right,
                lessThanOrEqualTo(viewport.right + .5),
                reason:
                    'character $i right must be visible without horizontal scrolling',
              );
              expect(
                rect.top,
                greaterThanOrEqualTo(viewport.top - .5),
                reason: 'character $i top',
              );
              expect(
                rect.bottom,
                lessThanOrEqualTo(viewport.bottom + .5),
                reason: 'character $i bottom must not be clipped',
              );
            }
          }
          final first = render
              .getBoxesForSelection(
                const TextSelection(baseOffset: 0, extentOffset: 1),
              )
              .single;
          final last = render
              .getBoxesForSelection(
                const TextSelection(
                  baseOffset: ip.length - 1,
                  extentOffset: ip.length,
                ),
              )
              .single;
          if (phone) {
            expect(
              last.top,
              greaterThan(first.top),
              reason: 'large phone text wraps onto another visible line',
            );
          } else {
            expect(
              last.top,
              first.top,
              reason: 'desktop text keeps its single line when it fits',
            );
          }
          await tester.longPress(find.text(ip));
          await tester.pumpAndSettle();
          expect(
            editable.textEditingValue.selection.isCollapsed,
            isFalse,
            reason: 'the full address remains selectable',
          );
          expect(tester.takeException(), isNull);
        },
      );
    }
  }
}

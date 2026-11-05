import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'ui/kit.dart';
import 'ui/settings_rows.dart';

/// Decorative drawing; it does not represent live connection telemetry.
/// FlClash's tone: a soft primary cookie behind a hairline globe, one solid
/// primary route between two round endpoints.
class ConnectionArtwork extends StatelessWidget {
  const ConnectionArtwork({super.key});

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final dark = s.brightness == Brightness.dark;
    return ExcludeSemantics(
      child: CustomPaint(
        painter: _NetworkPainter(
          primary: s.primary,
          wash: s.primary.withValues(alpha: dark ? .16 : .10),
          wire: s.primary.withValues(alpha: dark ? .45 : .30),
          knockout: context.surfaceLevel.colorOf(s),
        ),
        size: const Size(double.infinity, 230),
      ),
    );
  }
}

class _NetworkPainter extends CustomPainter {
  const _NetworkPainter({
    required this.primary,
    required this.wash,
    required this.wire,
    required this.knockout,
  });
  final Color primary, wash, wire, knockout;

  @override
  void paint(Canvas canvas, Size size) {
    final center = Offset(size.width * .5, size.height * .5);
    final radius = math.min(size.height * .40, size.width * .26);
    canvas.drawPath(
      settingsLobedShape(center, radius * 1.32, 9, .07),
      Paint()..color = wash,
    );
    final wirePaint = Paint()
      ..color = wire
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.2;
    canvas.drawCircle(center, radius, wirePaint);
    for (final ratio in [.38, .74]) {
      canvas.drawOval(
        Rect.fromCenter(
          center: center,
          width: radius * 2 * ratio,
          height: radius * 2,
        ),
        wirePaint,
      );
    }
    for (final dy in [-.55, 0.0, .55]) {
      final halfWidth = radius * math.sqrt(1 - dy * dy);
      canvas.drawOval(
        Rect.fromCenter(
          center: center + Offset(0, radius * dy),
          width: halfWidth * 2,
          height: radius * .22,
        ),
        wirePaint,
      );
    }
    final left = Offset(center.dx - radius - 30, center.dy + radius * .45);
    final right = Offset(center.dx + radius + 34, center.dy - radius * .42);
    final route = Paint()
      ..color = primary
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round
      ..strokeWidth = 2.5;
    canvas.drawPath(
      Path()
        ..moveTo(left.dx, left.dy)
        ..cubicTo(
          center.dx - radius * .9,
          center.dy - radius * 1.05,
          center.dx + radius * .45,
          center.dy + radius * .95,
          right.dx,
          right.dy,
        ),
      route,
    );
    final halo = Paint()..color = primary.withValues(alpha: .22);
    final dot = Paint()..color = primary;
    final hole = Paint()..color = knockout;
    for (final (point, r) in [
      (left, 7.0),
      (right, 7.0),
      (center + Offset(radius * .56, -radius * .34), 4.5),
    ]) {
      canvas.drawCircle(point, r + 6, halo);
      canvas.drawCircle(point, r, dot);
      canvas.drawCircle(point, r * .42, hole);
    }
  }

  @override
  bool shouldRepaint(_NetworkPainter old) =>
      primary != old.primary ||
      wash != old.wash ||
      wire != old.wire ||
      knockout != old.knockout;
}

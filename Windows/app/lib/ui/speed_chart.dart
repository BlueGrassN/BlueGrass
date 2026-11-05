import 'package:flutter/foundation.dart' show listEquals;
import 'package:flutter/material.dart';

/// An axis-free area chart of recent speeds, newest at the right edge.
///
/// Only the [values] given are drawn: with fewer than [capacity] samples the
/// curve starts part way across instead of being padded with invented zeros,
/// and with fewer than two nothing is drawn at all. The zero line is the
/// bottom edge, so a card can let the chart bleed past its edge to hide it.
class SpeedChart extends StatelessWidget {
  const SpeedChart({
    super.key,
    required this.values,
    required this.color,
    this.capacity = 30,
    this.minScale = 8 * 1024,
    this.strokeWidth = 2,
    this.fillOpacity = .14,
  }) : assert(capacity > 1),
       assert(minScale > 0);

  final List<double> values;
  final Color color;

  /// How many samples span the full width.
  final int capacity;

  /// The least value the top stands for, so idle noise stays low.
  final double minScale;
  final double strokeWidth;

  /// Opacity of the area under the line, relative to [color].
  final double fillOpacity;

  @override
  Widget build(BuildContext context) => RepaintBoundary(
    child: CustomPaint(
      painter: _SpeedChartPainter(
        values: List.unmodifiable(values),
        color: color,
        capacity: capacity,
        minScale: minScale,
        strokeWidth: strokeWidth,
        fillOpacity: fillOpacity,
      ),
      child: const SizedBox.expand(),
    ),
  );
}

class _SpeedChartPainter extends CustomPainter {
  _SpeedChartPainter({
    required this.values,
    required this.color,
    required this.capacity,
    required this.minScale,
    required this.strokeWidth,
    required this.fillOpacity,
  });

  final List<double> values;
  final Color color;
  final int capacity;
  final double minScale;
  final double strokeWidth;
  final double fillOpacity;

  @override
  void paint(Canvas canvas, Size size) {
    final count = values.length < capacity ? values.length : capacity;
    if (count < 2 || size.width <= 0 || size.height <= 0) return;
    final shown = values.sublist(values.length - count);
    var top = minScale;
    for (final value in shown) {
      if (value > top) top = value;
    }
    final step = size.width / (capacity - 1);
    final baseline = size.height;
    // Leave the stroke of the highest sample inside the chart.
    final usable = size.height - strokeWidth;
    final xs = [
      for (var i = 0; i < count; i++) size.width - (count - 1 - i) * step,
    ];
    final ys = [
      for (final value in shown)
        baseline - (value <= 0 ? 0 : value / top) * usable,
    ];
    double clampY(double y) =>
        y > baseline ? baseline : (y < strokeWidth / 2 ? strokeWidth / 2 : y);
    // Catmull-Rom through every sample; control points never dip below zero.
    final line = Path()..moveTo(xs.first, ys.first);
    for (var i = 0; i < count - 1; i++) {
      final before = i > 0 ? ys[i - 1] : ys[i];
      final after = i + 2 < count ? ys[i + 2] : ys[i + 1];
      line.cubicTo(
        xs[i] + step / 3,
        clampY(ys[i] + (ys[i + 1] - before) / 6),
        xs[i + 1] - step / 3,
        clampY(ys[i + 1] - (after - ys[i]) / 6),
        xs[i + 1],
        ys[i + 1],
      );
    }
    final area = Path.from(line)
      ..lineTo(xs.last, size.height)
      ..lineTo(xs.first, size.height)
      ..close();
    canvas
      ..save()
      ..clipRect(Offset.zero & size)
      ..drawPath(
        area,
        Paint()..color = color.withValues(alpha: color.a * fillOpacity),
      )
      ..drawPath(
        line,
        Paint()
          ..color = color
          ..style = PaintingStyle.stroke
          ..strokeWidth = strokeWidth
          ..strokeCap = StrokeCap.round
          ..strokeJoin = StrokeJoin.round,
      )
      ..restore();
  }

  @override
  bool shouldRepaint(_SpeedChartPainter oldDelegate) =>
      !listEquals(oldDelegate.values, values) ||
      oldDelegate.color != color ||
      oldDelegate.capacity != capacity ||
      oldDelegate.minScale != minScale ||
      oldDelegate.strokeWidth != strokeWidth ||
      oldDelegate.fillOpacity != fillOpacity;
}

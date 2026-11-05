import 'dart:math' as math;
import 'dart:typed_data';

import 'package:flutter/material.dart';

import 'kit.dart';

// Grouped settings rows in FlClash's list style, shared by the settings, about
// and diagnostics pages and the settings components (updates, crash reports,
// entry DNS): 56 dp rows, a 24 dp leading glyph, square rows inside a 24 dp
// group whose card clips the outer corners.

/// Start of a row title after a 24 dp leading glyph (16 + 24 + 16).
const double settingsTitleInset = 56;

/// A [SectionGroup] whose list tiles are square, at least 56 dp tall, so the
/// group's card alone rounds the first and last row.
class SettingsSection extends StatelessWidget {
  const SettingsSection({
    super.key,
    this.title,
    required this.children,
    this.footer,
    this.dividerIndent = settingsTitleInset,
  });

  final String? title;
  final List<Widget> children;
  final String? footer;

  /// Where the hairline between rows starts: the title's start.
  final double dividerIndent;

  @override
  Widget build(BuildContext context) => ListTileTheme.merge(
    shape: const RoundedRectangleBorder(),
    minTileHeight: 56,
    child: SectionGroup(
      title: title,
      footer: footer,
      dividerIndent: dividerIndent,
      children: children,
    ),
  );
}

/// A row whose control (a segmented choice) sits at its end when the row is
/// wide enough and under its title otherwise, filling the row.
class SettingsControlRow extends StatelessWidget {
  const SettingsControlRow({
    super.key,
    required this.icon,
    required this.title,
    this.subtitle,
    required this.control,
    this.inlineWidth = 560,
  });

  final IconData icon;
  final String title;
  final String? subtitle;

  /// Builds the control; `fill` is true when it takes the row's full width.
  final Widget Function(bool fill) control;

  /// The narrowest row that keeps the control on the title's line.
  final double inlineWidth;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final label = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          title,
          style: AppTextStyle.subheadline
              .of(context, color: s.onSurface)
              .copyWith(fontWeight: FontWeight.w500),
        ),
        if (subtitle != null)
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Text(
              subtitle!,
              style: AppTextStyle.footnote.of(
                context,
                color: s.onSurfaceVariant,
              ),
            ),
          ),
      ],
    );
    final glyph = Icon(icon, size: 24, color: s.onSurfaceVariant);
    return LayoutBuilder(
      builder: (context, constraints) {
        final inline = constraints.maxWidth >= inlineWidth;
        return ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 56),
          child: Padding(
            padding: EdgeInsets.fromLTRB(16, 10, 16, inline ? 10 : 14),
            child: inline
                ? Row(
                    children: [
                      glyph,
                      const SizedBox(width: 16),
                      Expanded(child: label),
                      const SizedBox(width: 16),
                      control(false),
                    ],
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        children: [
                          glyph,
                          const SizedBox(width: 16),
                          Expanded(child: label),
                        ],
                      ),
                      const SizedBox(height: 12),
                      Padding(
                        padding: const EdgeInsets.only(
                          left: settingsTitleInset - 16,
                        ),
                        child: control(true),
                      ),
                    ],
                  ),
          ),
        );
      },
    );
  }
}

/// FlClash's empty state: a soft primaryContainer shape holding the page's
/// glyph, a few tertiary dots around it, then a headline and a short line.
class SettingsEmptyState extends StatelessWidget {
  const SettingsEmptyState({
    super.key,
    required this.icon,
    required this.title,
    this.message,
    this.lobes = 8,
  });

  final IconData icon;
  final String title;
  final String? message;

  /// Lobes of the shape: 8 or 9 make a cookie, 4 a clover.
  final int lobes;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 24, horizontal: 16),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          SizedBox.square(
            dimension: 132,
            child: CustomPaint(
              painter: _EmptyShapePainter(
                fill: s.primaryContainer,
                dots: s.tertiary.withValues(alpha: .55),
                lobes: lobes,
              ),
              child: Center(
                child: Icon(icon, size: 44, color: s.onPrimaryContainer),
              ),
            ),
          ),
          const SizedBox(height: 16),
          Text(
            title,
            textAlign: TextAlign.center,
            style: AppTextStyle.headline.of(context, color: s.onSurface),
          ),
          if (message != null) ...[
            const SizedBox(height: 4),
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 360),
              child: Text(
                message!,
                textAlign: TextAlign.center,
                style: AppTextStyle.subheadline.of(
                  context,
                  color: s.onSurfaceVariant,
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// A closed shape whose radius swells [lobes] times around its centre.
Path settingsLobedShape(Offset center, double radius, int lobes, double depth) {
  final path = Path();
  const steps = 180;
  for (var i = 0; i <= steps; i++) {
    final t = i / steps * math.pi * 2;
    final r = radius * (1 - depth + depth * (math.cos(lobes * t) + 1) / 2);
    final point =
        center +
        Offset(math.cos(t - math.pi / 2), math.sin(t - math.pi / 2)) * r;
    if (i == 0) {
      path.moveTo(point.dx, point.dy);
    } else {
      path.lineTo(point.dx, point.dy);
    }
  }
  return path..close();
}

class _EmptyShapePainter extends CustomPainter {
  const _EmptyShapePainter({
    required this.fill,
    required this.dots,
    required this.lobes,
  });

  final Color fill;
  final Color dots;
  final int lobes;

  @override
  void paint(Canvas canvas, Size size) {
    final center = size.center(Offset.zero);
    final radius = size.shortestSide * .40;
    canvas.drawPath(
      settingsLobedShape(center, radius, lobes, lobes <= 4 ? .28 : .10),
      Paint()..color = fill,
    );
    final dot = Paint()..color = dots;
    for (final (angle, distance, r) in [
      (-.35, .50, 4.0),
      (.95, .49, 3.0),
      (2.55, .47, 5.0),
    ]) {
      canvas.drawCircle(
        center +
            Offset(math.cos(angle), math.sin(angle)) *
                (size.shortestSide * distance),
        r,
        dot,
      );
    }
  }

  @override
  bool shouldRepaint(_EmptyShapePainter old) =>
      old.fill != fill || old.dots != dots || old.lobes != lobes;
}

/// The BlueGrass app mark (assets/brand.svg): the grass glyph on its navy
/// rounded square, painted so it stays sharp at any size.
class BlueGrassMark extends StatelessWidget {
  const BlueGrassMark({super.key, this.size = 68});

  final double size;

  @override
  Widget build(BuildContext context) => Semantics(
    label: 'BlueGrass',
    image: true,
    child: DecoratedBox(
      decoration: ShapeDecoration(
        color: const Color(0xFF182B3A),
        shape: AppShape.all(size * .26),
      ),
      child: SizedBox.square(
        dimension: size,
        child: const CustomPaint(painter: _GrassPainter()),
      ),
    ),
  );
}

// assets/brand.svg's grass path, drawn in a 512 unit glyph box placed in the
// 44 unit icon by translate(2 42) scale(0.078125 -0.078125).
const _grass =
    'M256 85H43V128H165C150 188 103 235 43 250C56 254 71 256 85 256C180 256 '
    '256 180 256 85ZM469 250C456 254 441 256 427 256C364 256 310 222 280 172C286 '
    '158 291 143 294 128C297 114 298 100 298 85H469V128H346C362 188 409 235 469 '
    '250ZM334 277C350 321 381 359 421 384C329 381 256 306 256 213C276 240 303 262 '
    '334 277ZM244 323C226 370 189 408 143 427C174 387 192 337 192 284C192 279 191 '
    '275 191 271C200 265 209 259 217 253C222 278 231 302 244 323Z';

Path _parseGrass() {
  final path = Path();
  final tokens = RegExp(r'[MHVCZ]|-?\d*\.?\d+')
      .allMatches(_grass)
      .map((m) => m.group(0)!)
      .toList();
  var i = 0;
  var x = 0.0, y = 0.0;
  var command = 'M';
  double next() => double.parse(tokens[i++]);
  while (i < tokens.length) {
    final token = tokens[i];
    if (const {'M', 'H', 'V', 'C', 'Z'}.contains(token)) {
      command = token;
      i++;
      if (command == 'Z') path.close();
      continue;
    }
    switch (command) {
      case 'M':
        x = next();
        y = next();
        path.moveTo(x, y);
      case 'H':
        x = next();
        path.lineTo(x, y);
      case 'V':
        y = next();
        path.lineTo(x, y);
      case 'C':
        final x1 = next(), y1 = next(), x2 = next(), y2 = next();
        x = next();
        y = next();
        path.cubicTo(x1, y1, x2, y2, x, y);
      default:
        i++;
    }
  }
  const unit = 0.078125;
  return path.transform(
    Float64List.fromList([
      unit, 0, 0, 0, //
      0, -unit, 0, 0,
      0, 0, 1, 0,
      2, 42, 0, 1,
    ]),
  );
}

final Path _grassPath = _parseGrass();

class _GrassPainter extends CustomPainter {
  const _GrassPainter();

  @override
  void paint(Canvas canvas, Size size) {
    final scale = size.shortestSide / 44;
    canvas.save();
    canvas.scale(scale);
    canvas.drawPath(_grassPath, Paint()..color = const Color(0xFF67D2E5));
    canvas.restore();
  }

  @override
  bool shouldRepaint(_GrassPainter oldDelegate) => false;
}

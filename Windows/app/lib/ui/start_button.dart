import 'dart:math' as math;
import 'dart:ui' show lerpDouble;

import 'package:flutter/material.dart';

import 'kit.dart';

// The dashboard's expressive shapes (FlClash v0.9.2 draws them with M3
// Expressive polygons): a scalloped "cookie" whose lobes bulge out to the rim
// and whose valleys sit [depth] of the radius in. Re-drawn here as a polar
// curve, so a depth of 0 is an exact circle and every depth in between is a
// valid morph step.

/// A closed cookie outline inscribed in [rect]. One lobe points straight up
/// when [rotation] is 0; [sharpness] above 1 rounds the lobes off and pinches
/// the valleys, like the M3 cookie and clover shapes.
Path cookiePath(
  Rect rect, {
  required int lobes,
  required double depth,
  double rotation = 0,
  double sharpness = 1.3,
}) {
  if (depth <= 0 || lobes <= 0 || rect.isEmpty) return Path()..addOval(rect);
  final center = rect.center;
  final rx = rect.width / 2;
  final ry = rect.height / 2;
  final steps = lobes * 24;
  final path = Path();
  for (var i = 0; i < steps; i++) {
    final t = i / steps * 2 * math.pi;
    final wave = (1 - math.cos(lobes * t)) / 2;
    final r = 1 - depth * math.pow(wave, sharpness).toDouble();
    final a = t + rotation - math.pi / 2;
    final x = center.dx + rx * r * math.cos(a);
    final y = center.dy + ry * r * math.sin(a);
    if (i == 0) {
      path.moveTo(x, y);
    } else {
      path.lineTo(x, y);
    }
  }
  return path..close();
}

/// A cookie as an [OutlinedBorder], so a button, an ink splash and a shadow
/// can all take its outline. It lerps from a [CircleBorder] by growing its
/// valleys from nothing.
class CookieBorder extends OutlinedBorder {
  const CookieBorder({
    super.side,
    this.lobes = 9,
    this.depth = .12,
    this.rotation = 0,
    this.sharpness = 1.3,
  });

  final int lobes;
  final double depth;
  final double rotation;
  final double sharpness;

  Path _path(Rect rect) => cookiePath(
    rect,
    lobes: lobes,
    depth: depth,
    rotation: rotation,
    sharpness: sharpness,
  );

  @override
  EdgeInsetsGeometry get dimensions =>
      EdgeInsets.all(math.max(side.strokeInset, 0));

  @override
  Path getInnerPath(Rect rect, {TextDirection? textDirection}) =>
      _path(rect.deflate(math.max(side.strokeInset, 0)));

  @override
  Path getOuterPath(Rect rect, {TextDirection? textDirection}) => _path(rect);

  @override
  void paint(Canvas canvas, Rect rect, {TextDirection? textDirection}) {
    if (side.style == BorderStyle.none || side.width == 0) return;
    // The stroke centres on the line [BorderSide.strokeAlign] picks; an
    // align past 1 stands the ring off the shape.
    canvas.drawPath(_path(rect.inflate(side.strokeOffset / 2)), side.toPaint());
  }

  @override
  CookieBorder copyWith({
    BorderSide? side,
    int? lobes,
    double? depth,
    double? rotation,
    double? sharpness,
  }) => CookieBorder(
    side: side ?? this.side,
    lobes: lobes ?? this.lobes,
    depth: depth ?? this.depth,
    rotation: rotation ?? this.rotation,
    sharpness: sharpness ?? this.sharpness,
  );

  @override
  CookieBorder scale(double t) => copyWith(side: side.scale(t));

  @override
  ShapeBorder? lerpFrom(ShapeBorder? a, double t) {
    if (a is CookieBorder && a.lobes == lobes) {
      return CookieBorder(
        side: BorderSide.lerp(a.side, side, t),
        lobes: lobes,
        depth: lerpDouble(a.depth, depth, t)!,
        rotation: lerpDouble(a.rotation, rotation, t)!,
        sharpness: lerpDouble(a.sharpness, sharpness, t)!,
      );
    }
    if (a is CircleBorder) {
      return copyWith(
        side: BorderSide.lerp(a.side, side, t),
        depth: depth * t,
        rotation: rotation * t,
      );
    }
    return super.lerpFrom(a, t);
  }

  @override
  ShapeBorder? lerpTo(ShapeBorder? b, double t) {
    if (b is CookieBorder && b.lobes == lobes) return b.lerpFrom(this, t);
    if (b is CircleBorder) {
      return copyWith(
        side: BorderSide.lerp(side, b.side, t),
        depth: depth * (1 - t),
        rotation: rotation * (1 - t),
      );
    }
    return super.lerpTo(b, t);
  }

  @override
  bool operator ==(Object other) =>
      other is CookieBorder &&
      other.side == side &&
      other.lobes == lobes &&
      other.depth == depth &&
      other.rotation == rotation &&
      other.sharpness == sharpness;

  @override
  int get hashCode => Object.hash(side, lobes, depth, rotation, sharpness);

  @override
  String toString() =>
      'CookieBorder($side, lobes: $lobes, depth: $depth, rotation: $rotation)';
}

/// A filled cookie (or, at depth 0, a disc) with [child] upright on it.
class CookieBadge extends StatelessWidget {
  const CookieBadge({
    super.key,
    required this.size,
    required this.color,
    this.lobes = 6,
    this.depth = .14,
    this.rotation = 0,
    this.sharpness = 1.3,
    this.child,
  });

  final double size;
  final Color color;
  final int lobes;
  final double depth;
  final double rotation;
  final double sharpness;
  final Widget? child;

  @override
  Widget build(BuildContext context) => SizedBox.square(
    dimension: size,
    child: DecoratedBox(
      decoration: ShapeDecoration(
        color: color,
        shape: CookieBorder(
          lobes: lobes,
          depth: depth,
          rotation: rotation,
          sharpness: sharpness,
        ),
      ),
      child: child == null ? null : Center(child: child),
    ),
  );
}

/// The dashboard's start button: a primary disc with a play glyph while
/// idle that grows into a nine-lobed cookie, turning a ninth of a circle, once
/// [running]. It stays a [FilledButton] (with [label] under the glyph), so its
/// focus, keyboard activation and semantics are the button's own.
class StartToggleButton extends StatefulWidget {
  const StartToggleButton({
    super.key,
    required this.running,
    required this.onPressed,
    required this.icon,
    required this.label,
    this.size = 104,
    this.disabledColor,
  });

  final bool running;
  final VoidCallback? onPressed;
  final IconData icon;
  final String label;
  final double size;

  /// The fill while [onPressed] is null; the inset surface by default.
  final Color? disabledColor;

  @override
  State<StartToggleButton> createState() => _StartToggleButtonState();
}

class _StartToggleButtonState extends State<StartToggleButton>
    with SingleTickerProviderStateMixin {
  static const _morph = Duration(milliseconds: 450);
  static const _lobes = 9;
  static const _depth = .12;
  static const _turn = 2 * math.pi / _lobes;

  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: _morph,
    value: widget.running ? 1 : 0,
  );
  late final CurvedAnimation _progress = CurvedAnimation(
    parent: _controller,
    curve: Curves.easeOutBack,
    reverseCurve: Curves.easeInCubic,
  );
  final _states = WidgetStatesController();
  bool _pressed = false;

  @override
  void initState() {
    super.initState();
    _states.addListener(_syncPressed);
  }

  void _syncPressed() {
    final pressed = _states.value.contains(WidgetState.pressed);
    if (pressed != _pressed && mounted) setState(() => _pressed = pressed);
  }

  @override
  void didUpdateWidget(StartToggleButton oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.running == widget.running) return;
    if (MediaQuery.maybeDisableAnimationsOf(context) ?? false) {
      _controller.value = widget.running ? 1 : 0;
    } else if (widget.running) {
      _controller.forward();
    } else {
      _controller.reverse();
    }
  }

  @override
  void dispose() {
    _states
      ..removeListener(_syncPressed)
      ..dispose();
    _progress.dispose();
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final size = widget.size;
    final enabled = widget.onPressed != null;
    final disabledFill = widget.disabledColor ?? SurfaceLevel.inset.colorOf(s);
    Color ink(Set<WidgetState> states) => states.contains(WidgetState.disabled)
        ? s.onSurfaceVariant
        : s.onPrimary;
    final content = Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        AnimatedSwitcher(
          duration: const Duration(milliseconds: 200),
          transitionBuilder: (child, animation) => FadeTransition(
            opacity: animation,
            child: ScaleTransition(scale: animation, child: child),
          ),
          child: Icon(
            widget.icon,
            key: ValueKey(widget.icon),
            size: size * .34,
          ),
        ),
        SizedBox(
          width: size * .64,
          child: FittedBox(
            fit: BoxFit.scaleDown,
            child: Text(
              widget.label,
              maxLines: 1,
              softWrap: false,
              style: AppTextStyle.caption1.style.copyWith(
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ),
      ],
    );
    return AnimatedScale(
      scale: _pressed ? 1.05 : 1,
      duration: const Duration(milliseconds: 160),
      curve: Curves.easeOutBack,
      child: AnimatedBuilder(
        animation: _progress,
        child: content,
        builder: (context, child) {
          final progress = _progress.value;
          final shape = CookieBorder(
            lobes: _lobes,
            depth: math.max(0, _depth * progress),
            rotation: _turn * progress,
          );
          return DecoratedBox(
            decoration: ShapeDecoration(
              shape: shape,
              shadows: enabled
                  ? [
                      BoxShadow(
                        color: s.primary.withValues(alpha: .3),
                        offset: const Offset(0, 6),
                        blurRadius: 12,
                      ),
                    ]
                  : const [],
            ),
            child: FilledButton(
              statesController: _states,
              onPressed: widget.onPressed,
              style: ButtonStyle(
                fixedSize: WidgetStatePropertyAll(Size.square(size)),
                minimumSize: WidgetStatePropertyAll(Size.square(size)),
                maximumSize: WidgetStatePropertyAll(Size.square(size)),
                padding: const WidgetStatePropertyAll(EdgeInsets.zero),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                // The morph is driven here; the button must not tween it again.
                animationDuration: Duration.zero,
                elevation: const WidgetStatePropertyAll(0),
                shadowColor: const WidgetStatePropertyAll(Colors.transparent),
                splashFactory: NoSplash.splashFactory,
                shape: WidgetStatePropertyAll(shape),
                side: WidgetStateProperty.resolveWith(
                  (states) => states.contains(WidgetState.focused)
                      ? BorderSide(color: s.primary, width: 2, strokeAlign: 3)
                      : BorderSide.none,
                ),
                backgroundColor: WidgetStateProperty.resolveWith(
                  (states) => states.contains(WidgetState.disabled)
                      ? disabledFill
                      : s.primary,
                ),
                foregroundColor: WidgetStateProperty.resolveWith(ink),
                iconColor: WidgetStateProperty.resolveWith(ink),
                overlayColor: WidgetStateProperty.resolveWith(
                  (states) => states.contains(WidgetState.pressed)
                      ? s.onPrimary.withValues(alpha: .12)
                      : states.contains(WidgetState.focused)
                      ? s.onPrimary.withValues(alpha: .1)
                      : states.contains(WidgetState.hovered)
                      ? s.onPrimary.withValues(alpha: .08)
                      : Colors.transparent,
                ),
              ),
              child: child,
            ),
          );
        },
      ),
    );
  }
}

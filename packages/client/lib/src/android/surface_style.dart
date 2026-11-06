import 'package:flutter/material.dart';

/// Contact, ambient and lifted shadows are separate so the edges stay soft.
abstract final class SurfaceShadows {
  static List<BoxShadow> card(Brightness brightness) =>
      brightness == Brightness.dark
      ? const [
          BoxShadow(
            color: Color(0x66000000),
            offset: Offset(0, 1),
            blurRadius: 2,
          ),
          BoxShadow(
            color: Color(0x80000000),
            offset: Offset(0, 10),
            blurRadius: 24,
            spreadRadius: -6,
          ),
        ]
      : const [
          BoxShadow(
            color: Color(0x0d000000),
            offset: Offset(0, .5),
            blurRadius: 1,
          ),
          BoxShadow(
            color: Color(0x0a000000),
            offset: Offset(0, 2),
            blurRadius: 5,
          ),
          BoxShadow(
            color: Color(0x19000000),
            offset: Offset(0, 12),
            blurRadius: 27,
            spreadRadius: -6,
          ),
        ];

  static List<BoxShadow> floating(Brightness brightness) => [
    BoxShadow(
      color: Colors.black.withValues(
        alpha: brightness == Brightness.dark ? .5 : .08,
      ),
      offset: const Offset(0, 2),
      blurRadius: 4,
    ),
    BoxShadow(
      color: Colors.black.withValues(
        alpha: brightness == Brightness.dark ? .6 : .16,
      ),
      offset: const Offset(0, 12),
      blurRadius: 32,
      spreadRadius: -8,
    ),
  ];
}

/// The shadow stays outside the clipped material, including nested ink effects.
class SurfaceCard extends StatelessWidget {
  const SurfaceCard({
    super.key,
    required this.child,
    this.color,
    this.shape,
    this.margin = EdgeInsets.zero,
    this.clipBehavior = Clip.antiAlias,
  });
  final Widget child;
  final Color? color;
  final ShapeBorder? shape;
  final EdgeInsetsGeometry margin;
  final Clip clipBehavior;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final outline =
        shape ??
        RoundedSuperellipseBorder(
          borderRadius: BorderRadius.circular(28),
          side: BorderSide(
            color: Colors.white.withValues(
              alpha: theme.brightness == Brightness.dark ? .045 : .65,
            ),
            width: .6,
          ),
        );
    return Semantics(
      container: true,
      child: Container(
        margin: margin,
        decoration: ShapeDecoration(
          color: color ?? theme.colorScheme.surface,
          shape: outline,
          shadows: SurfaceShadows.card(theme.brightness),
        ),
        child: Material(
          type: MaterialType.transparency,
          // The decoration paints the rim once; this shape only clips ink.
          shape: outline is OutlinedBorder
              ? outline.copyWith(side: BorderSide.none)
              : outline,
          clipBehavior: clipBehavior,
          child: child,
        ),
      ),
    );
  }
}

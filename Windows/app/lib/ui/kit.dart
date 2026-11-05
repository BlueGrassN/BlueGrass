import 'package:flutter/material.dart';

// FlClash v0.9.2's visual language, re-implemented for BlueGrass: structure is
// drawn with fills that step in lightness, never with borders; colour marks
// state (selected, on, failing) and the one action a screen is for.

/// Depth of a surface. Each surface takes the level one deeper than the one it
/// sits on, which [SurfaceScope] passes down the tree.
enum SurfaceLevel {
  page,
  card,
  block,
  inset;

  SurfaceLevel get next => switch (this) {
    page => card,
    card => block,
    block || inset => inset,
  };

  /// Dialogs, menus and the dock: lighter than the cards they cover.
  static SurfaceLevel floatingOf(ColorScheme scheme) =>
      scheme.brightness == Brightness.dark ? block : card;

  Color colorOf(ColorScheme s) {
    final dark = s.brightness == Brightness.dark;
    return switch (this) {
      page => dark ? s.surface : s.surfaceContainer,
      card => dark ? s.surfaceContainer : s.surfaceBright,
      block => dark ? s.surfaceContainerHigh : s.surfaceContainer,
      inset => s.surfaceContainerHighest,
    };
  }
}

class SurfaceScope extends InheritedWidget {
  const SurfaceScope({super.key, required this.level, required super.child});

  final SurfaceLevel level;

  static SurfaceLevel of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<SurfaceScope>()?.level ??
      SurfaceLevel.page;

  @override
  bool updateShouldNotify(SurfaceScope oldWidget) => level != oldWidget.level;
}

extension SurfaceContext on BuildContext {
  SurfaceLevel get surfaceLevel => SurfaceScope.of(this);

  SurfaceLevel get raisedSurfaceLevel => surfaceLevel.next;

  /// The fill of a surface placed on the current one.
  Color get raisedSurfaceColor =>
      raisedSurfaceLevel.colorOf(Theme.of(this).colorScheme);
}

/// Fixed status palettes (green and orange tonal palettes, tone 40/80 for text,
/// 90/30 for containers) that ignore the seed, like Material's error.
extension BlueGrassSchemeColors on ColorScheme {
  bool get _dark => brightness == Brightness.dark;

  /// Window chrome: the sidebar, the dock pane and the title bar tint.
  Color get chrome => _dark ? surfaceContainerLow : surfaceContainerHighest;

  /// Every divider: translucent so it keeps one contrast on any level.
  Color get hairline => onSurface.withValues(alpha: .14);

  /// A tonal control one step above whatever surface it sits on.
  Color get tonalFill => onSurface.withValues(alpha: _dark ? .09 : .06);

  Color get success => Color(_dark ? 0xFF78DC77 : 0xFF006E1C);
  Color get successContainer => Color(_dark ? 0xFF005313 : 0xFF94F990);
  Color get onSuccessContainer => Color(_dark ? 0xFF94F990 : 0xFF005313);
  Color get warning => Color(_dark ? 0xFFFFB870 : 0xFF8B5000);
  Color get warningContainer => Color(_dark ? 0xFF693C00 : 0xFFFFDCBE);
  Color get onWarningContainer => Color(_dark ? 0xFFFFDCBE : 0xFF693C00);
}

abstract final class AppCorner {
  static const double xs = 4;
  static const double sm = 8;
  static const double md = 16;
  static const double lg = 20;
  static const double xl = 24;
  static const double xxl = 28;

  /// Alerts are rounder than sheets.
  static const double alert = 34;

  /// Dashboard cards are taller than any other surface and keep a larger
  /// corner; blocks inside them take [dashboardBlock].
  static const double dashboardCard = 32;
  static const double dashboardBlock = 20;
  static const double badge = 14;
}

abstract final class AppShape {
  static RoundedSuperellipseBorder all(double corner, {BorderSide? side}) =>
      RoundedSuperellipseBorder(
        borderRadius: BorderRadius.all(Radius.circular(corner)),
        side: side ?? BorderSide.none,
      );

  static const StadiumBorder pill = StadiumBorder();
}

/// Apple's Dynamic Type scale at its default size (FlClash's text roles), with
/// [display] for a dashboard figure and [pageTitle] for a page bar.
enum AppTextStyle {
  display(40, 44, emphasizedWeight: FontWeight.w700),
  largeTitle(34, 41, emphasizedWeight: FontWeight.w700),
  title1(28, 34, emphasizedWeight: FontWeight.w700),
  pageTitle(24, 30, weight: FontWeight.w600, emphasizedWeight: FontWeight.w700),
  title2(22, 28, emphasizedWeight: FontWeight.w700),
  title3(20, 25),
  headline(17, 22, weight: FontWeight.w600),
  body(17, 22),
  callout(16, 21),
  subheadline(15, 20),
  footnote(13, 18),
  caption1(12, 16),
  caption2(11, 13);

  const AppTextStyle(
    this.size,
    this.leading, {
    this.weight = FontWeight.w400,
    this.emphasizedWeight = FontWeight.w600,
  });

  final double size;
  final double leading;
  final FontWeight weight;
  final FontWeight emphasizedWeight;

  TextStyle get style => TextStyle(
    fontSize: size,
    height: leading / size,
    fontWeight: weight,
    letterSpacing: 0,
  );

  TextStyle of(BuildContext context, {bool emphasized = false, Color? color}) =>
      (Theme.of(context).textTheme.bodyMedium ?? const TextStyle()).copyWith(
        fontSize: size,
        height: leading / size,
        fontWeight: emphasized ? emphasizedWeight : weight,
        letterSpacing: 0,
        color: color,
      );
}

/// Soft shadows of the dashboard cards and floats (the design's CSS shadows
/// converted to Flutter blur radii).
abstract final class AppShadows {
  static BoxShadow _shadow(
    int alpha,
    double y,
    double cssBlur, [
    double spread = 0,
  ]) => BoxShadow(
    color: Color.fromARGB(alpha, 0, 0, 0),
    offset: Offset(0, y),
    blurRadius: cssBlur <= 1 ? 0 : (cssBlur / 2 - 0.5) / 0.57735,
    spreadRadius: spread,
  );

  static List<BoxShadow> e1(BuildContext context) =>
      Theme.of(context).brightness == Brightness.dark
      ? [_shadow(0x80, 1, 2), _shadow(0x8C, 10, 28, -6)]
      : [_shadow(0x0D, 0.5, 1), _shadow(0x0A, 2, 6), _shadow(0x17, 12, 32, -6)];

  static List<BoxShadow> e2(BuildContext context) =>
      Theme.of(context).brightness == Brightness.dark
      ? [_shadow(0x80, 2, 6), _shadow(0xB3, 32, 64, -12)]
      : [
          _shadow(0x0F, 1, 3),
          _shadow(0x14, 10, 24),
          _shadow(0x38, 32, 64, -12),
        ];
}

/// A raised surface: a card on the page, a block in a card. No border; the
/// fill is one level above the surface it sits on.
class BgCard extends StatelessWidget {
  const BgCard({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.all(16),
    this.radius = AppCorner.xl,
    this.onTap,
    this.color,
    this.shadow = false,
  });

  final Widget child;
  final EdgeInsetsGeometry padding;
  final double radius;
  final VoidCallback? onTap;
  final Color? color;
  final bool shadow;

  @override
  Widget build(BuildContext context) {
    final level = context.raisedSurfaceLevel;
    final shape = AppShape.all(radius);
    final fill = color ?? level.colorOf(Theme.of(context).colorScheme);
    final content = SurfaceScope(
      level: level,
      child: Padding(padding: padding, child: child),
    );
    final material = Material(
      color: fill,
      shape: shape,
      clipBehavior: Clip.antiAlias,
      child: onTap == null
          ? content
          : InkWell(customBorder: shape, onTap: onTap, child: content),
    );
    if (!shadow) return material;
    return DecoratedBox(
      decoration: ShapeDecoration(
        shape: shape,
        shadows: AppShadows.e1(context),
      ),
      child: material,
    );
  }
}

/// A dashboard card: card fill, 32 dp superellipse and the e1 shadow.
class DashCard extends StatelessWidget {
  const DashCard({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.symmetric(horizontal: 22, vertical: 20),
    this.onTap,
    this.clipBehavior = Clip.antiAlias,
  });

  final Widget child;
  final EdgeInsetsGeometry padding;
  final VoidCallback? onTap;
  final Clip clipBehavior;

  @override
  Widget build(BuildContext context) {
    final shape = AppShape.all(AppCorner.dashboardCard);
    final content = SurfaceScope(
      level: SurfaceLevel.card,
      child: Padding(padding: padding, child: child),
    );
    return DecoratedBox(
      decoration: ShapeDecoration(
        shape: shape,
        shadows: AppShadows.e1(context),
      ),
      child: Material(
        color: SurfaceLevel.card.colorOf(Theme.of(context).colorScheme),
        shape: shape,
        clipBehavior: clipBehavior,
        child: onTap == null
            ? content
            : InkWell(customBorder: shape, onTap: onTap, child: content),
      ),
    );
  }
}

/// Glyph, footnote-weight title and optional trailing marks of a card.
class DashCardHeader extends StatelessWidget {
  const DashCardHeader({
    super.key,
    this.icon,
    required this.title,
    this.leading = const [],
    this.trailing = const [],
  });

  final IconData? icon;
  final String title;
  final List<Widget> leading;
  final List<Widget> trailing;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return ConstrainedBox(
      constraints: const BoxConstraints(minHeight: 24),
      child: Row(
        spacing: 8,
        children: [
          if (icon != null)
            Icon(icon, size: 18, color: colors.onSurfaceVariant),
          Flexible(
            child: Text(
              title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyle.footnote.of(
                context,
                emphasized: true,
                color: colors.onSurfaceVariant,
              ),
            ),
          ),
          ...leading,
          if (trailing.isNotEmpty) ...[const Spacer(), ...trailing],
        ],
      ),
    );
  }
}

enum StatusTone { success, warning, error, neutral, primary }

/// A small status pill; colour goes on the chip, never on the whole card.
class StatusChip extends StatelessWidget {
  const StatusChip(
    this.label, {
    super.key,
    this.tone = StatusTone.neutral,
    this.dot = false,
  });

  final String label;
  final StatusTone tone;
  final bool dot;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final (background, foreground) = switch (tone) {
      StatusTone.success => (s.successContainer, s.onSuccessContainer),
      StatusTone.warning => (s.warningContainer, s.onWarningContainer),
      StatusTone.error => (s.errorContainer, s.onErrorContainer),
      StatusTone.primary => (s.primary, s.onPrimary),
      StatusTone.neutral => (context.raisedSurfaceColor, s.onSurfaceVariant),
    };
    return DecoratedBox(
      decoration: ShapeDecoration(color: background, shape: AppShape.pill),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          spacing: 5,
          children: [
            if (dot)
              Container(
                width: 6,
                height: 6,
                decoration: BoxDecoration(
                  color: tone == StatusTone.neutral ? s.outline : foreground,
                  shape: BoxShape.circle,
                ),
              ),
            Flexible(
              child: Text(
                label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: AppTextStyle.caption1.of(
                  context,
                  emphasized: true,
                  color: foreground,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A neutral well that only identifies: a country code, a letter or a glyph.
class GlyphWell extends StatelessWidget {
  const GlyphWell({super.key, this.text, this.icon, this.size = 40});

  final String? text;
  final IconData? icon;
  final double size;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return Container(
      width: size,
      height: size,
      alignment: Alignment.center,
      decoration: ShapeDecoration(
        color: context.raisedSurfaceColor,
        shape: AppShape.all(size * .35),
      ),
      child: icon != null
          ? Icon(icon, size: size * .5, color: s.onSurfaceVariant)
          : Text(
              text ?? '',
              maxLines: 1,
              style: AppTextStyle.footnote.of(
                context,
                emphasized: true,
                color: s.onSurfaceVariant,
              ),
            ),
    );
  }
}

/// A grouped run of rows: an optional footnote title, one raised card whose
/// rows are split by hairlines, and an optional caption footer.
class SectionGroup extends StatelessWidget {
  const SectionGroup({
    super.key,
    this.title,
    required this.children,
    this.footer,
    this.dividerIndent = 16,
    this.dividerEndIndent = 0,
  });

  final String? title;
  final List<Widget> children;
  final String? footer;

  /// Where the hairline starts: the title's leading edge (56 beside a
  /// 24 dp leading glyph).
  final double dividerIndent;
  final double dividerEndIndent;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (title != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Text(
              title!,
              style: AppTextStyle.footnote.of(
                context,
                emphasized: true,
                color: s.onSurfaceVariant,
              ),
            ),
          ),
        BgCard(
          padding: EdgeInsets.zero,
          radius: AppCorner.xl,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var i = 0; i < children.length; i++) ...[
                if (i > 0)
                  Divider(
                    height: 1,
                    thickness: 1,
                    indent: dividerIndent,
                    endIndent: dividerEndIndent,
                    color: s.hairline,
                  ),
                children[i],
              ],
            ],
          ),
        ),
        if (footer != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
            child: Text(
              footer!,
              style: AppTextStyle.caption1.of(
                context,
                color: s.onSurfaceVariant,
              ),
            ),
          ),
      ],
    );
  }
}

/// The page bar: a left-aligned page title and round tonal actions.
class PageHeader extends StatelessWidget {
  const PageHeader({
    super.key,
    required this.title,
    this.actions = const [],
    this.padding = const EdgeInsets.fromLTRB(24, 16, 20, 8),
  });

  final String title;
  final List<Widget> actions;
  final EdgeInsetsGeometry padding;

  @override
  Widget build(BuildContext context) => Padding(
    padding: padding,
    child: ConstrainedBox(
      constraints: const BoxConstraints(minHeight: 56),
      child: Row(
        spacing: 8,
        children: [
          Expanded(
            child: Text(
              title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyle.pageTitle.of(context),
            ),
          ),
          ...actions,
        ],
      ),
    ),
  );
}

/// A round tonal icon button for a page bar.
class HeaderAction extends StatelessWidget {
  const HeaderAction({
    super.key,
    required this.icon,
    required this.tooltip,
    required this.onPressed,
    this.busy = false,
  });

  final IconData icon;
  final String tooltip;
  final VoidCallback? onPressed;
  final bool busy;

  @override
  Widget build(BuildContext context) => IconButton(
    tooltip: tooltip,
    onPressed: onPressed,
    style: IconButton.styleFrom(
      backgroundColor: context.raisedSurfaceColor,
      fixedSize: const Size(44, 44),
      shape: const CircleBorder(),
    ),
    icon: busy
        ? const SizedBox(
            width: 18,
            height: 18,
            child: CircularProgressIndicator(strokeWidth: 2),
          )
        : Icon(icon, size: 20),
  );
}

/// An inline notice: a raised row with a glyph, tinted only by [tone].
class BgNotice extends StatelessWidget {
  const BgNotice(
    this.text, {
    super.key,
    this.icon = Icons.info_outline_rounded,
    this.tone = StatusTone.neutral,
  });

  final String text;
  final IconData icon;
  final StatusTone tone;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final (Color? fill, Color foreground) = switch (tone) {
      StatusTone.warning => (s.warningContainer, s.onWarningContainer),
      StatusTone.error => (s.errorContainer, s.onErrorContainer),
      StatusTone.success => (s.successContainer, s.onSuccessContainer),
      _ => (null, s.onSurface),
    };
    return BgCard(
      color: fill,
      radius: AppCorner.lg,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        spacing: 12,
        children: [
          Icon(
            icon,
            size: 20,
            color: fill == null ? s.onSurfaceVariant : foreground,
          ),
          Expanded(
            child: Text(
              text,
              style: AppTextStyle.footnote.of(context, color: foreground),
            ),
          ),
        ],
      ),
    );
  }
}

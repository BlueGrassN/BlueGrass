import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';

import 'kit.dart';

// Resource-page pieces in FlClash v0.9.2's language (group chips, filled
// search pills, node cards with a check badge, iOS-style alerts and the
// shape-and-glyph empty state), re-implemented for BlueGrass.

/// A [SegmentedButton] drawn as FlClash's run of group chips: every segment is
/// its own pill, the chosen one solid primary, the rest one surface level up.
/// The button keeps its segment semantics, focus traversal and keyboard use.
class ResChipSegments<T> extends StatelessWidget {
  const ResChipSegments({
    super.key,
    required this.segments,
    required this.selected,
    required this.onChanged,
    this.height = 48,
    this.gap = 8,
  });

  final List<(T, String)> segments;
  final T selected;

  /// Null disables every segment.
  final ValueChanged<T>? onChanged;
  final double height;
  final double gap;

  @override
  Widget build(BuildContext context) {
    final enabled = onChanged != null;
    final last = segments.length - 1;
    return SegmentedButton<T>(
      showSelectedIcon: false,
      expandedInsets: EdgeInsets.zero,
      style: ButtonStyle(
        // The faces paint fill, hover and focus; the button only lays out.
        backgroundColor: const WidgetStatePropertyAll(Colors.transparent),
        overlayColor: const WidgetStatePropertyAll(Colors.transparent),
        side: const WidgetStatePropertyAll(BorderSide.none),
        shape: const WidgetStatePropertyAll(StadiumBorder()),
        padding: const WidgetStatePropertyAll(EdgeInsets.zero),
        visualDensity: VisualDensity.standard,
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        splashFactory: NoSplash.splashFactory,
        textStyle: WidgetStatePropertyAll(
          AppTextStyle.subheadline.of(context, emphasized: true),
        ),
      ),
      segments: [
        for (final (index, (value, label)) in segments.indexed)
          ButtonSegment<T>(
            value: value,
            label: _ResChipFace(
              selected: value == selected,
              enabled: enabled,
              height: height,
              margin: EdgeInsetsDirectional.only(
                start: index == 0 ? 0 : gap / 2,
                end: index == last ? 0 : gap / 2,
              ),
              child: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
            ),
          ),
      ],
      selected: {selected},
      onSelectionChanged: enabled ? (values) => onChanged!(values.first) : null,
    );
  }
}

class _ResChipFace extends StatefulWidget {
  const _ResChipFace({
    required this.selected,
    required this.enabled,
    required this.height,
    required this.margin,
    required this.child,
  });

  final bool selected;
  final bool enabled;
  final double height;
  final EdgeInsetsGeometry margin;
  final Widget child;

  @override
  State<_ResChipFace> createState() => _ResChipFaceState();
}

class _ResChipFaceState extends State<_ResChipFace> {
  bool _hovered = false;
  bool _pressed = false;

  void _press(bool value) {
    if (_pressed != value) setState(() => _pressed = value);
  }

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final selected = widget.selected, enabled = widget.enabled;
    // The segment's own focus node; a ring only for keyboard focus.
    final focused =
        (Focus.maybeOf(context)?.hasPrimaryFocus ?? false) &&
        FocusManager.instance.highlightMode == FocusHighlightMode.traditional;
    final base = selected
        ? s.primary.withValues(alpha: enabled ? 1 : .6)
        : context.raisedSurfaceColor;
    final foreground = selected
        ? s.onPrimary
        : enabled
        ? s.onSurfaceVariant
        : s.onSurface.withValues(alpha: .38);
    final overlay = !enabled
        ? 0.0
        : _pressed
        ? .12
        : _hovered
        ? .08
        : 0.0;
    final fill = Color.alphaBlend(
      (selected ? s.onPrimary : s.onSurface).withValues(alpha: overlay),
      base,
    );
    return SizedBox(
      height: widget.height,
      child: Padding(
        padding: widget.margin,
        child: MouseRegion(
          onEnter: (_) => setState(() => _hovered = true),
          onExit: (_) => setState(() {
            _hovered = false;
            _pressed = false;
          }),
          child: Listener(
            onPointerDown: (_) => _press(true),
            onPointerUp: (_) => _press(false),
            onPointerCancel: (_) => _press(false),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 200),
              curve: Curves.easeOut,
              alignment: Alignment.center,
              padding: const EdgeInsets.symmetric(horizontal: 12),
              decoration: ShapeDecoration(
                color: fill,
                shape: StadiumBorder(
                  side: focused
                      ? BorderSide(
                          color: selected ? s.onSurface : s.primary,
                          width: 2,
                        )
                      : BorderSide.none,
                ),
              ),
              child: DefaultTextStyle.merge(
                style: TextStyle(color: foreground),
                child: widget.child,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A filled pill field as FlClash's search field: no outline at rest, a
/// primary ring only while focused, text centred in [height].
InputDecoration resPillDecoration(
  BuildContext context, {
  required String hint,
  IconData? icon,
  double height = 48,
}) {
  final s = Theme.of(context).colorScheme;
  final radius = BorderRadius.all(Radius.circular(height / 2));
  final rest = OutlineInputBorder(
    borderRadius: radius,
    borderSide: BorderSide.none,
  );
  return InputDecoration(
    constraints: BoxConstraints.tightFor(height: height),
    filled: true,
    fillColor: _resPillFill(context),
    hoverColor: s.onSurface.withValues(alpha: .04),
    hintText: hint,
    hintStyle: AppTextStyle.subheadline.of(context, color: s.onSurfaceVariant),
    floatingLabelBehavior: FloatingLabelBehavior.never,
    border: rest,
    enabledBorder: rest,
    disabledBorder: rest,
    focusedBorder: rest.copyWith(
      borderSide: BorderSide(color: s.primary, width: 2),
    ),
    contentPadding: EdgeInsetsDirectional.only(
      start: icon == null ? 18 : 0,
      end: 16,
    ),
    prefixIcon: icon == null
        ? null
        : Padding(
            padding: const EdgeInsetsDirectional.only(start: 14, end: 8),
            child: Icon(icon, size: 20),
          ),
    prefixIconConstraints: const BoxConstraints(),
  );
}

/// [resPillDecoration] for a [DropdownMenu], whose field it styles.
InputDecorationThemeData resPillDecorationTheme(
  BuildContext context, {
  double height = 48,
}) {
  final s = Theme.of(context).colorScheme;
  final radius = BorderRadius.all(Radius.circular(height / 2));
  final rest = OutlineInputBorder(
    borderRadius: radius,
    borderSide: BorderSide.none,
  );
  return InputDecorationThemeData(
    constraints: BoxConstraints.tightFor(height: height),
    filled: true,
    fillColor: _resPillFill(context),
    hoverColor: s.onSurface.withValues(alpha: .04),
    floatingLabelBehavior: FloatingLabelBehavior.never,
    border: rest,
    enabledBorder: rest,
    disabledBorder: rest,
    focusedBorder: rest.copyWith(
      borderSide: BorderSide(color: s.primary, width: 2),
    ),
    // A narrow trailing glyph leaves the value room at 130% text.
    contentPadding: const EdgeInsetsDirectional.only(start: 16),
    suffixIconConstraints: const BoxConstraints.tightFor(width: 40, height: 48),
    suffixIconColor: s.onSurfaceVariant,
  );
}

Color _resPillFill(BuildContext context) {
  final raised = context.raisedSurfaceColor;
  return WidgetStateColor.resolveWith(
    (states) => states.contains(WidgetState.disabled)
        ? raised.withValues(alpha: raised.a * .55)
        : raised,
  );
}

/// A resource name with its customer remark on the same line. The name keeps
/// its natural width while the remark can still show a few characters; only
/// then is the name shortened. A [Row] of two [Flexible]s split the line in
/// half and cut the name's IP even with room to spare.
class ResNameRemark extends MultiChildRenderObjectWidget {
  ResNameRemark({
    super.key,
    required Widget name,
    Widget? remark,
    this.gap = 12,
  }) : super(children: [name, ?remark]);

  final double gap;

  @override
  RenderObject createRenderObject(BuildContext context) =>
      _RenderResNameRemark(gap, Directionality.of(context));

  @override
  void updateRenderObject(BuildContext context, RenderObject renderObject) {
    (renderObject as _RenderResNameRemark)
      ..gap = gap
      ..textDirection = Directionality.of(context);
  }
}

class _ResNameRemarkParentData extends ContainerBoxParentData<RenderBox> {}

class _RenderResNameRemark extends RenderBox
    with
        ContainerRenderObjectMixin<RenderBox, _ResNameRemarkParentData>,
        RenderBoxContainerDefaultsMixin<RenderBox, _ResNameRemarkParentData> {
  _RenderResNameRemark(this._gap, this._textDirection);

  double _gap;
  set gap(double value) {
    if (value == _gap) return;
    _gap = value;
    markNeedsLayout();
  }

  TextDirection _textDirection;
  set textDirection(TextDirection value) {
    if (value == _textDirection) return;
    _textDirection = value;
    markNeedsLayout();
  }

  /// The remark keeps at least this much of the line before the name yields.
  static const double _remarkFloor = 48;

  @override
  void setupParentData(RenderBox child) {
    if (child.parentData is! _ResNameRemarkParentData) {
      child.parentData = _ResNameRemarkParentData();
    }
  }

  (double, double) _widths(double available) {
    final name = firstChild!, remark = childAfter(name)!;
    final n = name.getMaxIntrinsicWidth(double.infinity);
    final r = remark.getMaxIntrinsicWidth(double.infinity);
    if (!available.isFinite || n + _gap + r <= available) return (n, r);
    final floor = math.min(r, math.max(_remarkFloor, available * .3));
    final nameMax = math.max(0.0, math.min(n, available - _gap - floor));
    return (nameMax, math.max(0.0, available - _gap - nameMax));
  }

  Size _size(BoxConstraints constraints, ChildLayouter layoutChild) {
    final name = firstChild!, remark = childAfter(name);
    if (remark == null) {
      return constraints.constrain(
        layoutChild(name, BoxConstraints(maxWidth: constraints.maxWidth)),
      );
    }
    final (nameMax, _) = _widths(constraints.maxWidth);
    final nameSize = layoutChild(name, BoxConstraints(maxWidth: nameMax));
    final remarkMax = constraints.maxWidth.isFinite
        ? math.max(0.0, constraints.maxWidth - _gap - nameSize.width)
        : double.infinity;
    final remarkSize = layoutChild(remark, BoxConstraints(maxWidth: remarkMax));
    return constraints.constrain(
      Size(
        nameSize.width + _gap + remarkSize.width,
        math.max(nameSize.height, remarkSize.height),
      ),
    );
  }

  @override
  Size computeDryLayout(covariant BoxConstraints constraints) =>
      _size(constraints, ChildLayoutHelper.dryLayoutChild);

  @override
  void performLayout() {
    size = _size(constraints, ChildLayoutHelper.layoutChild);
    final name = firstChild!, remark = childAfter(name);
    final nameData = name.parentData! as _ResNameRemarkParentData;
    if (remark == null) {
      nameData.offset = Offset(
        _textDirection == TextDirection.rtl ? size.width - name.size.width : 0,
        0,
      );
      return;
    }
    final remarkData = remark.parentData! as _ResNameRemarkParentData;
    // Share one alphabetic baseline, as a baseline-aligned row does.
    final nameBaseline =
        name.getDistanceToBaseline(TextBaseline.alphabetic) ?? name.size.height;
    final remarkBaseline =
        remark.getDistanceToBaseline(TextBaseline.alphabetic) ??
        remark.size.height;
    final ascent = math.max(nameBaseline, remarkBaseline);
    final nameTop = ascent - nameBaseline, remarkTop = ascent - remarkBaseline;
    final height = math.max(
      nameTop + name.size.height,
      remarkTop + remark.size.height,
    );
    size = constraints.constrain(Size(size.width, height));
    final rtl = _textDirection == TextDirection.rtl;
    final remarkStart = name.size.width + _gap;
    nameData.offset = Offset(rtl ? size.width - name.size.width : 0, nameTop);
    remarkData.offset = Offset(
      rtl ? size.width - remarkStart - remark.size.width : remarkStart,
      remarkTop,
    );
  }

  double _sumWidth(double Function(RenderBox) of) {
    var total = 0.0;
    var count = 0;
    for (var child = firstChild; child != null; child = childAfter(child)) {
      total += of(child);
      count++;
    }
    return total + (count > 1 ? _gap : 0);
  }

  double _maxHeight(double Function(RenderBox) of) {
    var result = 0.0;
    for (var child = firstChild; child != null; child = childAfter(child)) {
      result = math.max(result, of(child));
    }
    return result;
  }

  @override
  double computeMinIntrinsicWidth(double height) =>
      _sumWidth((c) => c.getMinIntrinsicWidth(height));

  @override
  double computeMaxIntrinsicWidth(double height) =>
      _sumWidth((c) => c.getMaxIntrinsicWidth(height));

  @override
  double computeMinIntrinsicHeight(double width) =>
      _maxHeight((c) => c.getMinIntrinsicHeight(width));

  @override
  double computeMaxIntrinsicHeight(double width) =>
      _maxHeight((c) => c.getMaxIntrinsicHeight(width));

  @override
  double? computeDistanceToActualBaseline(TextBaseline baseline) =>
      defaultComputeDistanceToHighestActualBaseline(baseline);

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) =>
      defaultHitTestChildren(result, position: position);

  @override
  void paint(PaintingContext context, Offset offset) =>
      defaultPaint(context, offset);
}

/// FlClash's selection badge: a 22 dp primary disc with a check, ringed in
/// the colour of the surface it sits on so it reads over a glyph well.
class ResSelectionBadge extends StatelessWidget {
  const ResSelectionBadge({super.key, this.size = 22});

  final double size;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return Container(
      width: size + 4,
      height: size + 4,
      alignment: Alignment.center,
      decoration: ShapeDecoration(
        color: context.surfaceLevel.colorOf(s),
        shape: const CircleBorder(),
      ),
      child: Container(
        width: size,
        height: size,
        alignment: Alignment.center,
        decoration: ShapeDecoration(
          color: s.primary,
          shape: const CircleBorder(),
        ),
        child: Icon(Icons.check_rounded, size: size - 6, color: s.onPrimary),
      ),
    );
  }
}

/// A 40 dp country-code well; the chosen resource wears the check badge on
/// the well's corner.
class ResCountryBadge extends StatelessWidget {
  const ResCountryBadge({super.key, required this.code, this.selected = false});

  final String code;
  final bool selected;

  @override
  Widget build(BuildContext context) => SizedBox.square(
    dimension: 40,
    child: Stack(
      clipBehavior: Clip.none,
      children: [
        GlyphWell(text: code, size: 40),
        if (selected)
          const Positioned(top: -9, right: -9, child: ResSelectionBadge()),
      ],
    ),
  );
}

/// FlClash's empty state: no artwork, a cookie shape in primaryContainer with
/// the page glyph, a few tertiary specks, and the explanation underneath.
class ResEmptyState extends StatelessWidget {
  const ResEmptyState({
    super.key,
    required this.icon,
    required this.message,
    this.size = 88,
  });

  final IconData icon;
  final String message;
  final double size;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 28),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          SizedBox.square(
            dimension: size + 28,
            child: CustomPaint(
              painter: _ResCookiePainter(
                fill: s.primaryContainer,
                speck: s.tertiary,
                radius: size / 2,
              ),
              child: Center(
                child: Icon(icon, size: size * .4, color: s.onPrimaryContainer),
              ),
            ),
          ),
          const SizedBox(height: 12),
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 440),
            child: Text(
              message,
              textAlign: TextAlign.center,
              style: AppTextStyle.subheadline.of(
                context,
                color: s.onSurfaceVariant,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _ResCookiePainter extends CustomPainter {
  const _ResCookiePainter({
    required this.fill,
    required this.speck,
    required this.radius,
  });

  final Color fill;
  final Color speck;
  final double radius;

  @override
  void paint(Canvas canvas, Size size) {
    final center = size.center(Offset.zero);
    const lobes = 9, steps = 180;
    final path = Path();
    for (var i = 0; i <= steps; i++) {
      final t = i / steps * 2 * math.pi;
      final r = radius * (.92 + .08 * math.cos(lobes * t));
      final point =
          center +
          Offset(math.cos(t - math.pi / 2), math.sin(t - math.pi / 2)) * r;
      i == 0
          ? path.moveTo(point.dx, point.dy)
          : path.lineTo(point.dx, point.dy);
    }
    path.close();
    canvas.drawPath(path, Paint()..color = fill);
    final paint = Paint()..color = speck.withValues(alpha: .55);
    for (final (degrees, distance, dot) in [
      (-38.0, radius + 9, 3.5),
      (148.0, radius + 7, 2.5),
      (205.0, radius + 11, 3.0),
    ]) {
      final a = degrees * math.pi / 180;
      canvas.drawCircle(
        center + Offset(math.cos(a), math.sin(a)) * distance,
        dot,
        paint,
      );
    }
  }

  @override
  bool shouldRepaint(_ResCookiePainter old) =>
      old.fill != fill || old.speck != speck || old.radius != radius;
}

/// The indeterminate loader of a page: a 24 dp primary arc.
class ResLoading extends StatelessWidget {
  const ResLoading({super.key});

  @override
  Widget build(BuildContext context) => const Padding(
    padding: EdgeInsets.all(40),
    child: Center(
      child: SizedBox.square(
        dimension: 24,
        child: CircularProgressIndicator(strokeWidth: 2.5),
      ),
    ),
  );
}

/// An iOS-style alert as FlClash draws one: centred headline title, content
/// on the floating surface, 48 dp pill buttons with the last one the filled
/// default.
class ResAlert extends StatelessWidget {
  const ResAlert({
    super.key,
    required this.title,
    this.content,
    required this.actions,
    this.width = 400,
  });

  final String title;
  final Widget? content;
  final List<Widget> actions;
  final double width;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return AlertDialog(
      title: Text(title, textAlign: TextAlign.center),
      titleTextStyle: AppTextStyle.headline.of(context, color: s.onSurface),
      titlePadding: EdgeInsets.fromLTRB(24, 22, 24, content == null ? 22 : 0),
      contentPadding: const EdgeInsets.fromLTRB(20, 14, 20, 20),
      contentTextStyle: AppTextStyle.subheadline.of(
        context,
        color: s.onSurfaceVariant,
      ),
      content: content == null
          ? null
          : SurfaceScope(
              level: SurfaceLevel.floatingOf(s),
              child: SizedBox(width: width, child: content),
            ),
      actionsPadding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      actions: [ResAlertActions(children: actions)],
    );
  }
}

/// Alert buttons side by side, sharing the width.
class ResAlertActions extends StatelessWidget {
  const ResAlertActions({super.key, required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      for (final (index, child) in children.indexed) ...[
        if (index > 0) const SizedBox(width: 8),
        Expanded(child: child),
      ],
    ],
  );
}

/// A 48 dp alert pill: the default one filled primary, the others one level
/// above the alert.
class ResAlertButton extends StatelessWidget {
  const ResAlertButton({
    super.key,
    required this.label,
    required this.onPressed,
    this.isDefault = false,
    this.icon,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool isDefault;
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final fill = SurfaceLevel.floatingOf(s).next.colorOf(s);
    final style = TextButton.styleFrom(
      backgroundColor: isDefault ? s.primary : fill,
      foregroundColor: isDefault ? s.onPrimary : s.onSurface,
      iconColor: isDefault ? s.onPrimary : s.primary,
      minimumSize: const Size(0, 48),
      padding: const EdgeInsets.symmetric(horizontal: 16),
      shape: const StadiumBorder(),
      visualDensity: VisualDensity.standard,
      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
      textStyle: isDefault
          ? AppTextStyle.body.of(context, emphasized: true)
          : AppTextStyle.body.of(context).copyWith(fontWeight: FontWeight.w500),
    );
    final text = Text(label, maxLines: 1, overflow: TextOverflow.ellipsis);
    return icon == null
        ? TextButton(onPressed: onPressed, style: style, child: text)
        : TextButton.icon(
            onPressed: onPressed,
            style: style,
            icon: Icon(icon, size: 18),
            label: text,
          );
  }
}

/// Read-only fields grouped in one raised block, split by hairlines.
class ResFieldGroup extends StatelessWidget {
  const ResFieldGroup({super.key, required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return BgCard(
      radius: AppCorner.lg,
      padding: EdgeInsets.zero,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final (index, child) in children.indexed) ...[
            if (index > 0)
              Divider(height: 1, thickness: 1, indent: 16, color: s.hairline),
            child,
          ],
        ],
      ),
    );
  }
}

/// One read-only field: caption label over a selectable subheadline value,
/// with an optional caption note.
class ResField extends StatelessWidget {
  const ResField(this.label, this.value, {super.key, this.note});

  final String label;
  final String value;
  final String? note;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 11, 16, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: AppTextStyle.caption1.of(context, color: s.onSurfaceVariant),
          ),
          const SizedBox(height: 3),
          SelectableText(
            value.isEmpty ? '暂未提供' : value,
            style: AppTextStyle.subheadline.of(context, color: s.onSurface),
          ),
          if (note != null) ...[
            const SizedBox(height: 6),
            Text(
              note!,
              style: AppTextStyle.caption1.of(
                context,
                color: s.onSurfaceVariant,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

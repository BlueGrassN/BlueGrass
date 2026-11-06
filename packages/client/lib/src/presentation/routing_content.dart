import 'package:flutter/material.dart';

import '../../ui/kit.dart';

double _lerp(double a, double b, double t) => a + (b - a) * t;

/// Shared choices and lock semantics. The adapter controls when changing a
/// route is allowed; a presentation rebuild must never unlock an active tunnel.
class RoutingModeControl extends StatelessWidget {
  const RoutingModeControl({
    super.key,
    required this.value,
    required this.onChanged,
    this.expanded = false,
    this.height,
    this.outer = 24,
  });
  final String value;
  final ValueChanged<String>? onChanged;
  final bool expanded;
  final double? height;
  final double outer;
  static const modes = [
    ('bypass_cn', '国内外分流', Icons.rule_rounded),
    ('global', '全局代理', Icons.public_rounded),
  ];
  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, box) {
      final expressive = height != null;
      return SegmentedButton<String>(
        key: const Key('routing-mode'),
        direction:
            !expressive &&
                (box.maxWidth < 300 ||
                    MediaQuery.textScalerOf(context).scale(14) > 20)
            ? Axis.vertical
            : Axis.horizontal,
        expandedInsets: expressive || expanded ? EdgeInsets.zero : null,
        showSelectedIcon: false,
        style: !expressive
            ? null
            : const ButtonStyle(
                padding: WidgetStatePropertyAll(EdgeInsets.zero),
                backgroundColor: WidgetStatePropertyAll(Colors.transparent),
                overlayColor: WidgetStatePropertyAll(Colors.transparent),
                side: WidgetStatePropertyAll(BorderSide.none),
                shape: WidgetStatePropertyAll(RoundedRectangleBorder()),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                splashFactory: NoSplash.splashFactory,
              ),
        segments: [
          for (final (index, (id, label, icon)) in modes.indexed)
            ButtonSegment(
              value: id,
              icon: expressive ? null : Icon(icon),
              label: expressive
                  ? _RoutingSegmentFace(
                      label: label,
                      icon: icon,
                      selected: value == id,
                      enabled: onChanged != null,
                      first: index == 0,
                      last: index == modes.length - 1,
                      outer: outer,
                      height: height!,
                    )
                  : Text(label),
            ),
        ],
        selected: {value},
        onSelectionChanged: onChanged == null
            ? null
            : (values) => onChanged!(values.single),
      );
    },
  );
}

class RoutingModeExplanation extends StatelessWidget {
  const RoutingModeExplanation({
    super.key,
    required this.value,
    required this.saving,
    required this.enabled,
  });
  final String value;
  final bool saving, enabled;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        value == 'bypass_cn' ? '国内流量直连，国外流量通过所选节点。' : '国内、国外公网流量均通过所选节点。',
        style: Theme.of(context).textTheme.bodySmall
            ?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant),
      ),
      if (saving || !enabled) ...[
        const SizedBox(height: 8),
        Text(saving ? '正在保存…' : '断开连接后可切换分流模式。'),
      ],
    ],
  );
}

/// One segment of the routing switch, drawn as an M3 Expressive connected
/// button: tight inner corners that open into a full primary pill once it is
/// chosen. The [SegmentedButton] around it keeps selection, focus, keyboard
/// activation and semantics.
class _RoutingSegmentFace extends StatefulWidget {
  const _RoutingSegmentFace({
    required this.label,
    required this.icon,
    required this.selected,
    required this.enabled,
    required this.first,
    required this.last,
    required this.outer,
    required this.height,
  });

  final String label;
  final IconData icon;
  final bool selected;
  final bool enabled;
  final bool first;
  final bool last;
  final double outer;
  final double height;

  @override
  State<_RoutingSegmentFace> createState() => _RoutingSegmentFaceState();
}

class _RoutingSegmentFaceState extends State<_RoutingSegmentFace> {
  static const _inner = 10.0;
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final block = SurfaceLevel.block.colorOf(s);
    final card = SurfaceLevel.card.colorOf(s);
    final focused =
        widget.enabled &&
        (Focus.maybeOf(context)?.hasFocus ?? false) &&
        FocusManager.instance.highlightMode == FocusHighlightMode.traditional;
    final on = widget.enabled
        ? s.primary
        : Color.alphaBlend(s.primary.withValues(alpha: .32), card);
    final onInk = widget.enabled
        ? s.onPrimary
        : s.onSurface.withValues(alpha: .72);
    final offInk = widget.enabled
        ? s.onSurfaceVariant
        : s.onSurface.withValues(alpha: .38);
    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: TweenAnimationBuilder<double>(
        tween: Tween(end: widget.selected ? 1 : 0),
        duration: const Duration(milliseconds: 240),
        curve: Curves.easeOutCubic,
        builder: (context, t, _) {
          final start = _lerp(
            widget.first ? widget.outer : _inner,
            widget.outer,
            t,
          );
          final end = _lerp(
            widget.last ? widget.outer : _inner,
            widget.outer,
            t,
          );
          final ink = Color.lerp(offInk, onInk, t)!;
          var fill = Color.lerp(block, on, t)!;
          if (_hovered && widget.enabled) {
            fill = Color.alphaBlend(ink.withValues(alpha: .08), fill);
          }
          return Padding(
            padding: EdgeInsets.only(
              left: widget.first ? 0 : 1.5,
              right: widget.last ? 0 : 1.5,
            ),
            child: DecoratedBox(
              decoration: ShapeDecoration(
                color: fill,
                shape: RoundedSuperellipseBorder(
                  borderRadius: BorderRadius.horizontal(
                    left: Radius.circular(start),
                    right: Radius.circular(end),
                  ),
                  side: focused
                      ? BorderSide(
                          color: widget.selected ? s.onPrimary : s.primary,
                          width: 2,
                        )
                      : BorderSide.none,
                ),
              ),
              child: SizedBox(
                width: double.infinity,
                height: widget.height,
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 10),
                  child: Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      Icon(widget.icon, size: 18, color: ink),
                      const SizedBox(width: 6),
                      Flexible(
                        child: Text(
                          widget.label,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: AppTextStyle.subheadline.of(
                            context,
                            emphasized: true,
                            color: ink,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}

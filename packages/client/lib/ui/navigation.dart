import 'dart:math' as math;
import 'dart:ui' show lerpDouble;

import 'package:flutter/material.dart';

import 'kit.dart';

class AppDestination {
  const AppDestination({
    required this.icon,
    required this.selectedIcon,
    required this.label,
    this.key,
  });

  final IconData icon;
  final IconData selectedIcon;
  final String label;
  final Key? key;
}

bool usesNavigationDock(double width) => width < 840;

/// Shared responsive navigation. A Scaffold may own the dock separately so
/// snackbars and keyboard/system insets are laid out above it on touch hosts.
class ClientNavigationFrame extends StatelessWidget {
  const ClientNavigationFrame({
    super.key,
    required this.destinations,
    required this.selectedIndex,
    required this.onSelected,
    required this.child,
    this.expanded = true,
    this.onToggle,
    this.brand,
    this.footer,
    this.scaffoldOwnsDock = false,
  });
  final List<AppDestination> destinations;
  final int selectedIndex;
  final ValueChanged<int> onSelected;
  final Widget child;
  final bool expanded, scaffoldOwnsDock;
  final VoidCallback? onToggle;
  final Widget? brand;
  final Widget Function(BuildContext, bool)? footer;
  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, size) {
      if (usesNavigationDock(size.maxWidth)) {
        return Column(
          children: [
            Expanded(child: child),
            if (!scaffoldOwnsDock)
              AppDock(
                destinations: destinations,
                selectedIndex: selectedIndex,
                onSelected: onSelected,
              ),
          ],
        );
      }
      return Row(
        children: [
          AppSidebar(
            destinations: destinations,
            selectedIndex: selectedIndex,
            onSelected: onSelected,
            expanded: expanded,
            onToggle: onToggle,
            brand: brand,
            footer: footer,
          ),
          Expanded(child: child),
        ],
      );
    },
  );
}

const _compactWidth = 48.0;
const _expandedWidth = 220.0;
const _itemHeight = 40.0;
const _itemGap = 4.0;
const _sideInset = 4.0;
const _iconSize = 24.0;
const _labelGap = 12.0;
const _indicatorWidth = 3.0;
const _indicatorHeight = 16.0;
const _expandDuration = Duration(milliseconds: 250);
const _indicatorDuration = Duration(milliseconds: 320);

/// The desktop sidebar: window chrome fill, a hairline on its inner edge,
/// neutral selected fill and a primary bar that stretches toward a new row.
class AppSidebar extends StatelessWidget {
  const AppSidebar({
    super.key,
    required this.destinations,
    required this.selectedIndex,
    required this.onSelected,
    required this.expanded,
    this.onToggle,
    this.brand,
    this.footer,
  });

  final List<AppDestination> destinations;
  final int selectedIndex;
  final ValueChanged<int> onSelected;
  final bool expanded;

  /// Null where the window is too narrow for the labels.
  final VoidCallback? onToggle;
  final Widget? brand;
  final Widget Function(BuildContext context, bool expanded)? footer;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return TweenAnimationBuilder<double>(
      tween: Tween(end: expanded ? 1 : 0),
      duration: _expandDuration,
      curve: Easing.standard,
      builder: (context, progress, _) {
        final labelOpacity = ((progress - .4) / .6).clamp(0.0, 1.0);
        final width = lerpDouble(_compactWidth, _expandedWidth, progress)!;
        final rowWidth = _expandedWidth - _sideInset * 2;
        final iconInset = (_compactWidth - _sideInset * 2 - _iconSize) / 2;
        return Material(
          color: colors.chrome,
          shape: Border(right: BorderSide(color: colors.hairline)),
          child: SizedBox(
            width: width,
            child: ClipRect(
              child: OverflowBox(
                alignment: AlignmentDirectional.topStart,
                minWidth: width,
                maxWidth: width,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    const SizedBox(height: 12),
                    SizedBox(
                      height: _itemHeight,
                      child: Row(
                        children: [
                          if (onToggle != null)
                            _SidebarButton(
                              tooltip: expanded ? '收起侧栏' : '展开侧栏',
                              onTap: onToggle!,
                              child: SizedBox(
                                width: _compactWidth - _sideInset * 2,
                                child: Center(
                                  child: Transform.flip(
                                    flipX: true,
                                    child: Icon(
                                      Icons.view_sidebar_outlined,
                                      size: _iconSize,
                                      color: colors.onSurfaceVariant,
                                    ),
                                  ),
                                ),
                              ),
                            )
                          else
                            const SizedBox(width: _sideInset),
                          if (brand != null && labelOpacity > 0)
                            Expanded(
                              child: Opacity(
                                opacity: labelOpacity,
                                child: Padding(
                                  padding: const EdgeInsets.only(left: 4),
                                  child: Align(
                                    alignment: Alignment.centerLeft,
                                    child: brand,
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 16),
                    Expanded(
                      child: SingleChildScrollView(
                        child: Stack(
                          children: [
                            Column(
                              crossAxisAlignment: CrossAxisAlignment.stretch,
                              children: [
                                for (var i = 0; i < destinations.length; i++)
                                  _SidebarButton(
                                    key:
                                        destinations[i].key ??
                                        ValueKey(destinations[i].label),
                                    selected: i == selectedIndex,
                                    tooltip: progress == 1
                                        ? ''
                                        : destinations[i].label,
                                    onTap: () => onSelected(i),
                                    child: _DestinationRow(
                                      destination: destinations[i],
                                      selected: i == selectedIndex,
                                      width: rowWidth,
                                      iconInset: iconInset,
                                      labelOpacity: labelOpacity,
                                    ),
                                  ),
                              ],
                            ),
                            if (selectedIndex >= 0 &&
                                selectedIndex < destinations.length)
                              _SelectionIndicator(
                                color: colors.primary,
                                index: selectedIndex,
                                start: _sideInset,
                                stride: _itemHeight + _itemGap,
                              ),
                          ],
                        ),
                      ),
                    ),
                    if (footer != null) footer!(context, progress == 1),
                    const SizedBox(height: 12),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}

class _SidebarButton extends StatelessWidget {
  const _SidebarButton({
    super.key,
    required this.tooltip,
    required this.onTap,
    required this.child,
    this.selected = false,
  });

  final String tooltip;
  final VoidCallback onTap;
  final Widget child;
  final bool selected;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final shape = AppShape.all(10);
    return Padding(
      padding: const EdgeInsets.symmetric(
        horizontal: _sideInset,
        vertical: _itemGap / 2,
      ),
      child: Semantics(
        button: true,
        selected: selected,
        child: Material(
          color: selected
              ? colors.onSurface.withValues(alpha: .08)
              : Colors.transparent,
          shape: shape,
          child: InkWell(
            onTap: onTap,
            customBorder: shape,
            splashFactory: NoSplash.splashFactory,
            overlayColor: WidgetStateProperty.resolveWith(
              (states) => states.contains(WidgetState.pressed)
                  ? colors.onSurface.withValues(alpha: .06)
                  : states.contains(WidgetState.focused)
                  ? colors.onSurface.withValues(alpha: .1)
                  : states.contains(WidgetState.hovered)
                  ? colors.onSurface.withValues(alpha: .04)
                  : Colors.transparent,
            ),
            child: Tooltip(
              message: tooltip,
              child: SizedBox(height: _itemHeight, child: child),
            ),
          ),
        ),
      ),
    );
  }
}

/// Lays out at the expanded width whatever the pane's width, so the label is
/// clipped rather than reflowed while the pane animates.
class _DestinationRow extends StatelessWidget {
  const _DestinationRow({
    required this.destination,
    required this.selected,
    required this.width,
    required this.iconInset,
    required this.labelOpacity,
  });

  final AppDestination destination;
  final bool selected;
  final double width;
  final double iconInset;
  final double labelOpacity;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return ClipRect(
      child: OverflowBox(
        alignment: AlignmentDirectional.centerStart,
        minWidth: width,
        maxWidth: width,
        child: Row(
          children: [
            SizedBox(width: iconInset),
            Icon(
              selected ? destination.selectedIcon : destination.icon,
              size: _iconSize,
              color: selected ? colors.onSurface : colors.onSurfaceVariant,
            ),
            const SizedBox(width: _labelGap),
            Expanded(
              child: Opacity(
                opacity: labelOpacity,
                alwaysIncludeSemantics: true,
                child: Text(
                  destination.label,
                  maxLines: 1,
                  softWrap: false,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyle.subheadline.of(
                    context,
                    emphasized: selected,
                    color: colors.onSurface,
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SelectionIndicator extends StatefulWidget {
  const _SelectionIndicator({
    required this.color,
    required this.index,
    required this.start,
    required this.stride,
  });

  final Color color;
  final int index;
  final double start;
  final double stride;

  @override
  State<_SelectionIndicator> createState() => _SelectionIndicatorState();
}

class _SelectionIndicatorState extends State<_SelectionIndicator>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: _indicatorDuration,
    value: 1,
  );
  late int _from = widget.index;

  @override
  void didUpdateWidget(covariant _SelectionIndicator oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.index != widget.index) {
      _from = oldWidget.index;
      _controller.forward(from: 0);
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  double _topOf(int index) =>
      index * widget.stride + (widget.stride - _indicatorHeight) / 2;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _controller,
    builder: (context, child) {
      final t = _controller.value;
      final from = _topOf(_from);
      final to = _topOf(widget.index);
      final lead = Curves.easeOutCubic.transform(math.min(1, t / .6));
      final trail = Curves.easeInOutCubic.transform(
        math.max(0, (t - .35) / .65),
      );
      final (top, bottom) = to >= from
          ? (lerpDouble(from, to, trail)!, lerpDouble(from, to, lead)!)
          : (lerpDouble(from, to, lead)!, lerpDouble(from, to, trail)!);
      return Positioned(
        left: widget.start,
        top: top,
        width: _indicatorWidth,
        height: bottom - top + _indicatorHeight,
        child: child!,
      );
    },
    child: IgnorePointer(
      child: DecoratedBox(
        decoration: ShapeDecoration(color: widget.color, shape: AppShape.pill),
      ),
    ),
  );
}

/// The narrow-window dock: a floating pill over the page with a neutral
/// platter behind the selected destination, whose icon and label take primary.
class AppDock extends StatelessWidget {
  const AppDock({
    super.key,
    required this.destinations,
    required this.selectedIndex,
    required this.onSelected,
  });

  final List<AppDestination> destinations;
  final int selectedIndex;
  final ValueChanged<int> onSelected;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final level = SurfaceLevel.floatingOf(colors);
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 4, 12, 12),
        child: DecoratedBox(
          decoration: ShapeDecoration(
            shape: AppShape.pill,
            shadows: AppShadows.e2(context),
          ),
          child: Material(
            color: level.colorOf(colors),
            shape: AppShape.pill,
            clipBehavior: Clip.antiAlias,
            child: Padding(
              padding: const EdgeInsets.all(4),
              child: Row(
                children: [
                  for (var i = 0; i < destinations.length; i++)
                    Expanded(
                      child: _DockItem(
                        destination: destinations[i],
                        selected: i == selectedIndex,
                        platter: level.next.colorOf(colors),
                        onTap: () => onSelected(i),
                      ),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _DockItem extends StatelessWidget {
  const _DockItem({
    required this.destination,
    required this.selected,
    required this.platter,
    required this.onTap,
  });

  final AppDestination destination;
  final bool selected;
  final Color platter;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final color = selected ? colors.primary : colors.onSurfaceVariant;
    return Semantics(
      button: true,
      selected: selected,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        decoration: ShapeDecoration(
          color: selected ? platter : platter.withValues(alpha: 0),
          shape: AppShape.pill,
        ),
        child: InkWell(
          key: destination.key,
          customBorder: AppShape.pill,
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  selected ? destination.selectedIcon : destination.icon,
                  size: 22,
                  color: color,
                ),
                const SizedBox(height: 2),
                Text(
                  destination.label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyle.caption2.of(
                    context,
                    emphasized: true,
                    color: color,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

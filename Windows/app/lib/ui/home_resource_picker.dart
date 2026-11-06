import 'dart:math' as math;

import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:flutter/material.dart';

import '../app_controller.dart';
import '../resource_presentation.dart';
import 'kit.dart';

/// Returns a choice only. The caller retains confirmation and connection logic.
Future<domain.NodeRecord?> showHomeResourcePicker({
  required BuildContext context,
  required Rect anchor,
  required AppController controller,
}) => showDialog<domain.NodeRecord>(
  context: context,
  barrierColor: Colors.black.withValues(alpha: .08),
  builder: (context) => Padding(
    padding: MediaQuery.viewInsetsOf(context),
    child: CustomSingleChildLayout(
      delegate: _PickerPosition(anchor),
      child: _ResourcePicker(controller: controller),
    ),
  ),
);

class _PickerPosition extends SingleChildLayoutDelegate {
  const _PickerPosition(this.anchor);
  final Rect anchor;
  static const margin = 12.0;
  static const gap = 8.0;

  @override
  BoxConstraints getConstraintsForChild(BoxConstraints constraints) {
    final width = math.min(
      440.0,
      math.max(0.0, constraints.maxWidth - margin * 2),
    );
    final room = math.max(0.0, constraints.maxHeight - margin * 2);
    final below = constraints.maxHeight - anchor.bottom - gap - margin;
    final above = anchor.top - gap - margin;
    final height = math.min(
      420.0,
      math.min(room, math.max(180.0, math.max(below, above))),
    );
    return BoxConstraints(minWidth: width, maxWidth: width, maxHeight: height);
  }

  @override
  Offset getPositionForChild(Size size, Size childSize) {
    final left = anchor.left.clamp(
      margin,
      math.max(margin, size.width - childSize.width - margin),
    );
    var top = anchor.bottom + gap;
    if (top + childSize.height > size.height - margin) {
      top = anchor.top - gap - childSize.height;
    }
    top = top.clamp(
      margin,
      math.max(margin, size.height - childSize.height - margin),
    );
    return Offset(left.toDouble(), top);
  }

  @override
  bool shouldRelayout(_PickerPosition oldDelegate) =>
      oldDelegate.anchor != anchor;
}

class _ResourcePicker extends StatefulWidget {
  const _ResourcePicker({required this.controller});
  final AppController controller;
  @override
  State<_ResourcePicker> createState() => _ResourcePickerState();
}

class _ResourcePickerState extends State<_ResourcePicker> {
  final _search = TextEditingController();
  late final String? _account;
  bool _closing = false;
  AppController get c => widget.controller;

  @override
  void initState() {
    super.initState();
    _account = c.customer?.id;
    c.addListener(_changed);
  }

  void _changed() {
    if (!mounted) return;
    if (c.customer?.id != _account && !_closing) {
      _closing = true;
      final route = ModalRoute.of(context);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && route != null && route.isActive) {
          // A notification can open another dialog above us. Remove this
          // picker only, including its barrier, without popping that dialog.
          Navigator.of(context).removeRoute(route);
        }
      });
    }
    setState(() {});
  }

  @override
  void dispose() {
    c.removeListener(_changed);
    _search.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_closing) return const SizedBox.shrink();
    final colors = Theme.of(context).colorScheme;
    final now = c.effectiveNowUtc;
    final query = _search.text.trim().toLowerCase();
    final nodes = c.nodes
        .where(
          (node) =>
              domain.isResourceVisible(node.expiresAtUtc, now) &&
              [
                node.name,
                node.countryName,
                node.city,
                node.customerRemark,
                node.key.sourceId,
              ].join(' ').toLowerCase().contains(query),
        )
        .toList();
    return Semantics(
      scopesRoute: true,
      namesRoute: true,
      label: '选择连接资源',
      explicitChildNodes: true,
      child: Material(
        key: const ValueKey('home-resource-picker'),
        elevation: 8,
        shadowColor: Colors.black.withValues(alpha: .24),
        color: SurfaceLevel.floatingOf(colors).colorOf(colors),
        surfaceTintColor: Colors.transparent,
        shape: AppShape.all(AppCorner.xl),
        clipBehavior: Clip.antiAlias,
        child: SurfaceScope(
          level: SurfaceLevel.floatingOf(colors),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 8, 0),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        '选择连接资源',
                        style: AppTextStyle.headline.of(context),
                      ),
                    ),
                    IconButton(
                      tooltip: '刷新资源列表',
                      icon: const Icon(Icons.refresh_rounded),
                      onPressed: c.loading ? null : () => c.refresh(),
                    ),
                    IconButton(
                      tooltip: '关闭资源选择',
                      icon: const Icon(Icons.close_rounded),
                      onPressed: () => Navigator.of(context).pop(),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 4, 12, 12),
                child: TextField(
                  key: const ValueKey('home-resource-search'),
                  controller: _search,
                  autofocus: true,
                  onChanged: (_) => setState(() {}),
                  style: AppTextStyle.subheadline.of(context),
                  decoration: InputDecoration(
                    hintText: '搜索名称、地区或备注',
                    isDense: true,
                    filled: true,
                    fillColor: colors.tonalFill,
                    prefixIcon: const Icon(Icons.search_rounded),
                    suffixIcon: query.isEmpty
                        ? null
                        : IconButton(
                            tooltip: '清空搜索',
                            icon: const Icon(Icons.close_rounded),
                            onPressed: () => setState(_search.clear),
                          ),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(24),
                      borderSide: BorderSide.none,
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(24),
                      borderSide: BorderSide.none,
                    ),
                  ),
                ),
              ),
              if (c.loading) const LinearProgressIndicator(minHeight: 2),
              Flexible(
                child: nodes.isEmpty
                    ? SingleChildScrollView(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Text(
                            c.loading
                                ? '正在加载资源…'
                                : query.isEmpty
                                ? '暂无连接资源，请刷新后重试'
                                : '没有匹配的已加载资源',
                            textAlign: TextAlign.center,
                            style: AppTextStyle.footnote.of(
                              context,
                              color: colors.onSurfaceVariant,
                            ),
                          ),
                        ),
                      )
                    : ListView.builder(
                        shrinkWrap: true,
                        padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
                        itemCount: nodes.length,
                        itemBuilder: (context, index) {
                          final node = nodes[index];
                          final unavailable = resourceUnavailableReason(
                            node,
                            now,
                          );
                          final selected = node.key == c.selected?.key;
                          return Padding(
                            padding: const EdgeInsets.only(bottom: 4),
                            child: ListTile(
                              enabled: !c.busy && unavailable == null,
                              selected: selected,
                              selectedColor: colors.onSurface,
                              selectedTileColor: colors.tonalFill,
                              shape: AppShape.all(AppCorner.md),
                              leading: GlyphWell(
                                text: node.countryCode.isEmpty
                                    ? null
                                    : node.countryCode,
                                icon: node.countryCode.isEmpty
                                    ? Icons.public_rounded
                                    : null,
                                size: 36,
                              ),
                              title: Text(
                                node.name,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                              subtitle: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(
                                    [
                                      node.countryName,
                                      node.protocol.toUpperCase(),
                                      node.customerRemark,
                                    ].where((s) => s.isNotEmpty).join(' · '),
                                    maxLines: 2,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                  if (unavailable != null)
                                    Text(
                                      unavailable,
                                      maxLines: 2,
                                      overflow: TextOverflow.ellipsis,
                                      style: AppTextStyle.caption1.of(
                                        context,
                                        color: colors.error,
                                      ),
                                    ),
                                ],
                              ),
                              trailing: selected
                                  ? Icon(
                                      Icons.check_circle_rounded,
                                      color: colors.primary,
                                      size: 22,
                                    )
                                  : null,
                              onTap: c.busy || unavailable != null
                                  ? null
                                  : () => Navigator.of(context).pop(node),
                            ),
                          );
                        },
                      ),
              ),
              if (c.message != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                  child: Text(
                    c.message!,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyle.caption1.of(
                      context,
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                ),
              if (c.hasMore)
                Padding(
                  padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
                  child: TextButton.icon(
                    onPressed: c.loading ? null : () => c.refresh(more: true),
                    icon: const Icon(Icons.expand_more_rounded),
                    label: const Text('加载更多资源'),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

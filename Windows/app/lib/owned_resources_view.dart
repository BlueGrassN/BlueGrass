import 'dart:async';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';

import 'resource_presentation.dart';
import 'ui/kit.dart';

/// Account-owned, read-only queries. Neither list nor detail grants connection
/// eligibility. The shell keys this widget by source and account session.
class OwnedResourcesView extends StatefulWidget {
  const OwnedResourcesView({
    super.key,
    required this.source,
    required this.accountKey,
    required this.onDataFailure,
    required this.openWebsite,
    this.isCurrentAccount,
    this.refreshRevision = 0,
    this.effectiveNowUtc,
    this.searchQuery = '',
  });
  final CustomerDataSource source;
  final String accountKey;
  final void Function(Object) onDataFailure;
  final VoidCallback openWebsite;
  // The shell's account object changes on re-login, even for the same account
  // id. Check before forwarding failures, including before the next UI frame.
  final bool Function()? isCurrentAccount;
  final int refreshRevision;
  final DateTime Function()? effectiveNowUtc;
  final String searchQuery;
  @override
  State<OwnedResourcesView> createState() => _OwnedResourcesViewState();
}

class _OwnedPage {
  final items = <ResourceKey, OwnedResourceRecord>{};
  final cursors = <String>{};
  String? cursor, error;
  bool loaded = false, more = false, blocked = false;
  int pages = 0, attempts = 0;
}

class _OwnedResourcesViewState extends State<OwnedResourcesView> {
  static const _pageSize = 25, _maxPages = 40, _maxAttempts = 60;
  static const _timeout = Duration(seconds: 15);
  Map<String, _OwnedPage> pages = {};
  String kind = 'subscription';
  bool busy = false;
  bool? supported;
  String? error, detailError;
  ResourceKey? detailKey;
  OwnedResourceRecord? detail;
  int generation = 0;
  String get kindLabel => kind == 'subscription' ? '订阅' : '资产';
  _OwnedPage get current => pages[kind]!;
  bool _current(int token) =>
      mounted &&
      token == generation &&
      (widget.isCurrentAccount?.call() ?? true) &&
      widget.source.customer?.id == widget.accountKey;

  @override
  void initState() {
    super.initState();
    _reset();
  }

  @override
  void didUpdateWidget(OwnedResourcesView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.source, widget.source) ||
        oldWidget.accountKey != widget.accountKey ||
        oldWidget.refreshRevision != widget.refreshRevision && !busy) {
      _reset();
    }
  }

  void _reset() {
    generation++;
    pages = {'subscription': _OwnedPage(), 'asset': _OwnedPage()};
    supported = null;
    error = null;
    detail = null;
    detailKey = null;
    detailError = null;
    busy = true;
    unawaited(_initialize(generation));
  }

  Future<void> _initialize(int token) async {
    try {
      final capabilities = await widget.source.refreshCapabilities().timeout(
        _timeout,
      );
      if (!_current(token)) return;
      setState(() {
        supported = capabilities.resourceQueries;
        busy = false;
      });
      if (supported == true) await _load();
    } catch (failure) {
      if (!_current(token)) return;
      setState(() {
        busy = false;
        error = '资源查询未完成，请刷新重试；不能据此判断账号没有资源。';
      });
      widget.onDataFailure(failure);
    }
  }

  void _validate(
    OwnedResourceRecord record,
    String expectedKind, {
    ResourceKey? key,
  }) {
    final summary = record.summary;
    ResourceKey.fromJson(summary.key.toJson());
    if (summary.key.accountKey != widget.accountKey ||
        summary.key.kind != expectedKind ||
        !summary.owned ||
        key != null && summary.key != key) {
      throw const ApiFailure('invalid_response');
    }
  }

  Future<void> _load() async {
    final page = current, requestedKind = kind, token = generation;
    if (busy ||
        supported != true ||
        page.blocked ||
        page.loaded && !page.more ||
        !_current(token)) {
      return;
    }
    if (page.pages >= _maxPages || page.attempts >= _maxAttempts) {
      setState(() {
        page.blocked = true;
        page.error = '已达到本轮查询上限；当前只显示已加载资源，可刷新重新查询。';
      });
      return;
    }
    page.attempts++;
    setState(() {
      busy = true;
      page.error = null;
    });
    try {
      final result = await widget.source
          .listResources(
            kind: requestedKind,
            cursor: page.cursor,
            perPage: _pageSize,
          )
          .timeout(_timeout);
      if (!_current(token)) return;
      final next = result.nextCursor;
      if (result.kind != requestedKind ||
          result.items.length > _pageSize ||
          result.hasMore &&
              (next == null || next.isEmpty || page.cursors.contains(next)) ||
          !result.hasMore && next != null) {
        page.blocked = true;
        throw const FormatException('RESOURCE_PAGINATION_INVALID');
      }
      for (final item in result.items) {
        _validate(item, requestedKind);
      }
      setState(() {
        for (final item in result.items) {
          page.items[item.summary.key] = item;
        }
        page.loaded = true;
        page.pages++;
        page.more = result.hasMore;
        page.cursor = next;
        if (next != null) page.cursors.add(next);
      });
    } catch (failure) {
      if (!_current(token)) return;
      setState(() {
        page.error = page.blocked
            ? '分页异常，已停止继续加载；请刷新重试。'
            : '资源查询未完成；已显示内容可能过时，请重试。';
      });
      widget.onDataFailure(failure);
    } finally {
      if (_current(token)) setState(() => busy = false);
    }
  }

  Future<void> _showDetails(ResourceKey key) async {
    final token = generation;
    if (busy || !_current(token)) return;
    setState(() {
      busy = true;
      detailKey = key;
      detail = null;
      detailError = null;
    });
    try {
      final fresh = await widget.source
          .getResource(key.sourceId, kind: key.kind)
          .timeout(_timeout);
      if (!_current(token)) return;
      _validate(fresh, key.kind, key: key);
      setState(() => detail = fresh);
    } catch (failure) {
      if (!_current(token)) return;
      setState(() => detailError = '无法确认此资源当前归属或状态，请刷新后重试。');
      widget.onDataFailure(failure);
    } finally {
      if (_current(token)) setState(() => busy = false);
    }
  }

  @override
  void dispose() {
    generation++;
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (!_current(generation)) {
      return const SizedBox.shrink();
    }
    final s = Theme.of(context).colorScheme;
    final page = current;
    final search = widget.searchQuery.trim().toLowerCase();
    final now = widget.effectiveNowUtc?.call() ?? DateTime.now().toUtc();
    final rows = page.items.values
        .where((item) => isResourceVisible(item.summary.expiresAtUtc, now))
        .where(
          (item) =>
              '${item.summary.name} ${item.summary.key.sourceId} ${item.status}'
                  .toLowerCase()
                  .contains(search),
        )
        .toList();
    final detailRecord = detail ?? page.items[detailKey];
    final showDetail =
        detailKey != null &&
        (detailRecord == null ||
            isResourceVisible(detailRecord.summary.expiresAtUtc, now));
    final secondary = AppTextStyle.footnote.of(
      context,
      color: s.onSurfaceVariant,
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text(
            '部分同步：仅显示当前查询到的归属资源，不代表账号全部资产。分页结束也不代表完整覆盖。',
            style: secondary,
          ),
        ),
        const SizedBox(height: 12),
        if (error != null) ...[
          BgNotice(
            error!,
            icon: Icons.error_outline_rounded,
            tone: StatusTone.warning,
          ),
          const SizedBox(height: 12),
        ],
        if (supported == false) ...[
          const BgNotice('当前服务未开放资源查询；可连接列表为空不代表没有购买资源。'),
          const SizedBox(height: 12),
        ],
        if (supported == true) ...[
          Align(
            alignment: AlignmentDirectional.centerStart,
            child: SizedBox(
              width: 184,
              height: 40,
              child: ResChipSegments<String>(
                height: 40,
                segments: const [('subscription', '订阅'), ('asset', '资产')],
                selected: kind,
                onChanged: busy
                    ? null
                    : (value) {
                        setState(() {
                          kind = value;
                          detailKey = null;
                          detail = null;
                          detailError = null;
                        });
                        if (!current.loaded) unawaited(_load());
                      },
              ),
            ),
          ),
          const SizedBox(height: 12),
          if (page.error != null) ...[
            BgNotice(
              page.error!,
              icon: Icons.error_outline_rounded,
              tone: StatusTone.warning,
            ),
            const SizedBox(height: 12),
          ],
          if (page.loaded && rows.isEmpty)
            ResEmptyState(
              icon: search.isEmpty
                  ? Icons.inventory_2_outlined
                  : Icons.search_off_rounded,
              message: search.isEmpty ? '已加载资源中没有可显示的资源。' : '已加载资源中没有匹配结果。',
            ),
          for (final row in rows)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: BgCard(
                radius: AppCorner.lg,
                padding: EdgeInsets.zero,
                child: ListTile(
                  contentPadding: const EdgeInsetsDirectional.fromSTEB(
                    16,
                    2,
                    16,
                    2,
                  ),
                  minVerticalPadding: 12,
                  horizontalTitleGap: 14,
                  titleAlignment: ListTileTitleAlignment.center,
                  leading: GlyphWell(
                    icon: row.summary.key.kind == 'asset'
                        ? Icons.inventory_2_outlined
                        : Icons.autorenew_rounded,
                  ),
                  title: Text(
                    row.summary.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyle.subheadline.of(
                      context,
                      emphasized: true,
                      color: s.onSurface,
                    ),
                  ),
                  subtitle: Padding(
                    padding: const EdgeInsets.only(top: 4),
                    child: Text(
                      '编号 ${row.summary.key.sourceId} · 服务状态：${row.status}\n${formatExpiry(row.summary.expiresAtUtc)}',
                      style: secondary,
                    ),
                  ),
                  isThreeLine: true,
                  trailing: Icon(
                    Icons.info_outline_rounded,
                    color: s.onSurfaceVariant,
                  ),
                  onTap: busy ? null : () => _showDetails(row.summary.key),
                ),
              ),
            ),
          if (showDetail) ...[
            const SizedBox(height: 4),
            BgCard(
              padding: const EdgeInsets.fromLTRB(20, 18, 20, 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const DashCardHeader(
                    icon: Icons.info_outline_rounded,
                    title: '只读资源详情',
                  ),
                  if (detail != null) ...[
                    const SizedBox(height: 12),
                    Text(
                      detail!.summary.name,
                      style: AppTextStyle.headline.of(
                        context,
                        color: s.onSurface,
                      ),
                    ),
                    if (detail!.summary.customerRemark.isNotEmpty) ...[
                      const SizedBox(height: 4),
                      Text(
                        '客户备注：${detail!.summary.customerRemark}',
                        style: secondary,
                      ),
                    ],
                    const SizedBox(height: 12),
                    SizedBox(
                      width: double.infinity,
                      child: BgCard(
                        radius: AppCorner.md,
                        padding: const EdgeInsets.symmetric(
                          horizontal: 14,
                          vertical: 12,
                        ),
                        child: DefaultTextStyle.merge(
                          style: AppTextStyle.footnote.of(
                            context,
                            color: s.onSurface,
                          ),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            spacing: 4,
                            children: [
                              Text(
                                '${detail!.summary.key.kind} / ${detail!.summary.key.sourceId}',
                              ),
                              Text('服务状态：${detail!.status}'),
                              Text(formatExpiry(detail!.summary.expiresAtUtc)),
                            ],
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(height: 12),
                    Text(
                      '此页仅查询归属和业务状态，不代表连接资格。购买或续费请前往官网。',
                      style: AppTextStyle.caption1.of(
                        context,
                        color: s.onSurfaceVariant,
                      ),
                    ),
                  ],
                  if (detailError != null) ...[
                    const SizedBox(height: 12),
                    BgNotice(
                      detailError!,
                      icon: Icons.error_outline_rounded,
                      tone: StatusTone.error,
                    ),
                  ],
                  if (busy && detail == null) ...[
                    const SizedBox(height: 12),
                    Text('正在核实资源详情…', style: secondary),
                  ],
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      if (detail != null)
                        TextButton.icon(
                          onPressed: widget.openWebsite,
                          icon: const Icon(Icons.open_in_new_rounded, size: 16),
                          label: const Text('前往官网'),
                        ),
                      OutlinedButton(
                        onPressed: busy
                            ? null
                            : () => setState(() {
                                detailKey = null;
                                detail = null;
                                detailError = null;
                              }),
                        child: const Text('关闭详情'),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            const SizedBox(height: 8),
          ],
          if (!page.blocked && (page.more || !page.loaded))
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Center(
                child: OutlinedButton(
                  onPressed: busy ? null : _load,
                  child: Text(
                    page.error != null ? '重试查询$kindLabel' : '加载更多$kindLabel',
                  ),
                ),
              ),
            ),
        ],
        if (busy)
          Padding(
            padding: const EdgeInsets.all(12),
            child: Text(
              '正在查询资源…',
              textAlign: TextAlign.center,
              style: secondary,
            ),
          ),
        if (error != null || supported == false || page.blocked)
          Center(
            child: TextButton(
              onPressed: busy ? null : () => setState(_reset),
              child: const Text('重新查询资源'),
            ),
          ),
      ],
    );
  }
}

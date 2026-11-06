part of '../main.dart';

extension _ResourcesPage on _ShellState {
  Widget _resources() {
    final account = c.customer!;
    final colors = Theme.of(context).colorScheme;
    final query = search.text.trim().toLowerCase();
    final now = c.effectiveNowUtc;
    final visible = c.nodes
        .where((node) => domain.isResourceVisible(node.expiresAtUtc, now))
        .toList();
    final filtered = visible
        .where(
          (n) =>
              (country == '全部地区' || n.countryName == country) &&
              '${n.name} ${n.countryName} ${n.city} ${n.key.sourceId}'
                  .toLowerCase()
                  .contains(query),
        )
        .map(
          (node) =>
              (node: node, unavailable: resourceUnavailableReason(node, now)),
        )
        .toList();
    final countries = [
      '全部地区',
      ...visible.map((n) => n.countryName).where((n) => n.isNotEmpty).toSet(),
    ];
    final filtering = query.isNotEmpty || country != '全部地区';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Wrap(
          spacing: 8,
          runSpacing: 8,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            SizedBox(
              key: const ValueKey('resource-scope-control'),
              width: 232,
              height: 48,
              child: ResChipSegments<bool>(
                segments: const [(false, '连接资源'), (true, '全部资源')],
                selected: allResources,
                onChanged: (value) => _update(() => allResources = value),
              ),
            ),
            SizedBox(
              key: const ValueKey('resource-search-control'),
              width: 232,
              height: 48,
              child: TextField(
                controller: search,
                onChanged: (_) => _update(() {}),
                textAlignVertical: TextAlignVertical.center,
                style: AppTextStyle.subheadline.of(context),
                decoration: resPillDecoration(
                  context,
                  hint: '搜索已加载资源',
                  icon: Icons.search_rounded,
                ),
              ),
            ),
            SizedBox(
              key: const ValueKey('resource-country-control'),
              width: 150,
              height: 48,
              child: Tooltip(
                message: allResources ? '全部资源暂不支持地区筛选' : '',
                child: DropdownMenu<String>(
                  enabled: !allResources,
                  initialSelection: allResources ? '全部地区' : country,
                  width: 150,
                  textStyle: AppTextStyle.subheadline.of(
                    context,
                    color: allResources
                        ? colors.onSurfaceVariant
                        : colors.onSurface,
                  ),
                  trailingIcon: const Icon(Icons.unfold_more_rounded, size: 20),
                  selectedTrailingIcon: const Icon(
                    Icons.unfold_less_rounded,
                    size: 20,
                  ),
                  inputDecorationTheme: resPillDecorationTheme(context),
                  dropdownMenuEntries: [
                    for (final name in countries)
                      DropdownMenuEntry(value: name, label: name),
                  ],
                  onSelected: (v) => _update(() => country = v ?? '全部地区'),
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        if (allResources)
          OwnedResourcesView(
            key: ValueKey((c.source, account)),
            source: c.source,
            accountKey: account.id,
            isCurrentAccount: () => identical(c.customer, account),
            refreshRevision: _ownedRefresh,
            searchQuery: search.text,
            effectiveNowUtc: () => c.effectiveNowUtc,
            onDataFailure: c.handleDataFailure,
            openWebsite: _openWebsite,
          )
        else if (filtered.isEmpty && c.loading)
          const ResLoading()
        else if (filtered.isEmpty)
          ResEmptyState(
            icon: c.hasMore || filtering
                ? Icons.search_off_rounded
                : Icons.public_rounded,
            message: c.hasMore
                ? '当前页没有匹配资源，可继续加载后续页面。'
                : filtering
                ? '已加载的资源中没有匹配结果。'
                : '服务暂未返回连接资源。你可以刷新，或查看“全部资源”。',
          )
        else
          for (final (:node, :unavailable) in filtered)
            _resNodeCard(node, unavailable),
        if (!allResources && c.hasMore)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Center(
              child: OutlinedButton(
                onPressed: c.loading ? null : () => c.refresh(more: true),
                child: Text(c.loading ? '正在加载' : '加载更多'),
              ),
            ),
          ),
        const SizedBox(height: 12),
        Center(
          child: TextButton.icon(
            onPressed: _openWebsite,
            icon: const Icon(Icons.open_in_new_rounded, size: 16),
            label: const Text('前往官网购买或续费'),
          ),
        ),
      ],
    );
  }

  Widget _resNodeCard(domain.NodeRecord node, String? unavailable) => Padding(
    padding: const EdgeInsets.only(bottom: 8),
    child: ResourceContent(
      data: ResourceViewData(
        name: node.name,
        remark: node.customerRemark,
        metadata:
            '${node.countryName} · ${node.protocol.toUpperCase()}\n到期：${_expiry(node.expiresAtUtc)}',
        countryCode: node.countryCode.isEmpty ? 'IP' : node.countryCode,
        selected: c.selected?.key == node.key,
        unavailable: unavailable,
      ),
      onSelect: unavailable == null ? () => _select(node) : null,
      onDetails: () => _showDetails(node),
    ),
  );

  void _showDetails(domain.NodeRecord node) => showDialog<void>(
    context: context,
    builder: (context) => ListenableBuilder(
      listenable: c,
      builder: (context, _) =>
          !domain.isResourceVisible(node.expiresAtUtc, c.effectiveNowUtc)
          ? ResAlert(
              title: '资源已超出显示期限',
              actions: [
                ResAlertButton(
                  label: '关闭',
                  isDefault: true,
                  onPressed: () => Navigator.pop(context),
                ),
              ],
            )
          : ResourceDetails(
              node: node,
              actualExitIp: c.connected && c.selected?.key == node.key
                  ? c.actualExitIp
                  : null,
              openWebsite: () => _openWebsite(),
            ),
    ),
  );
}

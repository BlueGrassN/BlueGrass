import 'package:flutter/material.dart';

import '../../presentation/resource_content.dart';
import '../app_controller.dart';
import '../resource_presentation.dart';
import 'components.dart';

class ResourcesScreen extends StatefulWidget {
  const ResourcesScreen({super.key, required this.controller});
  final AppController controller;
  @override
  State<ResourcesScreen> createState() => _ResourcesScreenState();
}

class _ResourcesScreenState extends State<ResourcesScreen> {
  String _query = '';
  @override
  Widget build(BuildContext context) {
    final state = widget.controller;
    final theme = Theme.of(context);
    final items = state.resources
        .where(
          (r) =>
              '${r.name} ${r.remark} ${r.location} ${r.protocol ?? ''} ${r.key.sourceId}'
                  .toLowerCase()
                  .contains(_query.toLowerCase()),
        )
        .toList();
    return RefreshIndicator(
      onRefresh: state.refresh,
      child: ListView(
        padding: EdgeInsets.fromLTRB(
          16,
          8,
          16,
          120 + MediaQuery.paddingOf(context).bottom,
        ),
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          TextField(
            key: const Key('resource-search'),
            decoration: const InputDecoration(
              hintText: '搜索已加载资源',
              prefixIcon: Icon(Icons.search_rounded),
            ),
            onChanged: (value) => setState(() => _query = value),
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              SelectionButton(
                label: '连接资源',
                selected: state.view == ResourceView.connectable,
                onPressed: () => state.changeView(ResourceView.connectable),
              ),
              SelectionButton(
                label: '全部资源',
                selected: state.view != ResourceView.connectable,
                onPressed: state.resourceQueries
                    ? () => state.changeView(ResourceView.subscriptions)
                    : null,
              ),
            ],
          ),
          if (state.view != ResourceView.connectable) ...[
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final (view, label) in [
                  (ResourceView.subscriptions, '订阅'),
                  (ResourceView.assets, '资产'),
                ])
                  SelectionButton(
                    label: label,
                    selected: state.view == view,
                    onPressed: state.resourceQueries
                        ? () => state.changeView(view)
                        : null,
                  ),
              ],
            ),
          ],
          const SizedBox(height: 12),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 4),
            child: Text(
              '已加载 ${state.resources.length} 项${state.hasMore ? ' · 还有更多' : ''}',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          if (state.view != ResourceView.connectable) ...[
            const SizedBox(height: 12),
            const NoticeCard('展示服务端当前可确认归属的资源；列表可能不完整。需要连接时，请在“连接资源”中选择。'),
          ],
          if (state.loading)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 16),
              child: LinearProgressIndicator(),
            ),
          if (state.resourceError != null) ...[
            const SizedBox(height: 12),
            NoticeCard(
              state.resourceError!,
              error: true,
              onRetry: state.refresh,
            ),
          ],
          if (items.isEmpty && !state.loading && state.resourceError == null)
            Padding(
              padding: const EdgeInsets.only(top: 16),
              child: SurfaceCard(
                margin: EdgeInsets.zero,
                child: Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 24,
                    vertical: 32,
                  ),
                  child: Column(
                    children: [
                      Icon(
                        Icons.dns_outlined,
                        size: 40,
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      const SizedBox(height: 16),
                      Text(
                        _query.isNotEmpty
                            ? '已加载资源中没有匹配项'
                            : state.hasMore
                            ? '本页没有可展示的资源'
                            : state.view == ResourceView.connectable
                            ? '暂无可连接资源'
                            : '暂无可展示的资源',
                        textAlign: TextAlign.center,
                        style: theme.textTheme.titleSmall,
                      ),
                      if (state.view == ResourceView.connectable &&
                          _query.isEmpty)
                        Padding(
                          padding: const EdgeInsets.only(top: 8),
                          child: Text(
                            '可查看全部订阅与资产，或刷新重试。',
                            textAlign: TextAlign.center,
                            style: theme.textTheme.bodyMedium?.copyWith(
                              color: theme.colorScheme.onSurfaceVariant,
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ),
          if (items.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: LayoutBuilder(
                builder: (context, constraints) {
                  const spacing = 12.0;
                  final twoColumns =
                      constraints.maxWidth >= 350 &&
                      MediaQuery.textScalerOf(context).scale(16) <= 16 * 1.3;
                  final tileWidth = twoColumns
                      ? (constraints.maxWidth - spacing) / 2
                      : constraints.maxWidth;
                  return Wrap(
                    spacing: spacing,
                    runSpacing: spacing,
                    children: [
                      for (final item in items)
                        SizedBox(
                          width: tileWidth,
                          child: _ResourceCard(
                            item: item,
                            selected: state.selected?.key == item.key,
                            onSelect: () => state.select(item),
                            controller: state,
                          ),
                        ),
                    ],
                  );
                },
              ),
            ),
          if (state.hasMore)
            Padding(
              padding: const EdgeInsets.only(top: 20),
              child: OutlinedButton(
                onPressed: state.loading ? null : state.loadMore,
                child: const Text('加载更多'),
              ),
            ),
          const SizedBox(height: 14),
          TextButton.icon(
            onPressed: state.openOfficialWebsite,
            icon: const Icon(Icons.open_in_new),
            label: const Text('前往官网购买或续费'),
          ),
          if (state.websiteError != null) ...[
            const SizedBox(height: 12),
            NoticeCard(state.websiteError!, error: true),
          ],
        ],
      ),
    );
  }
}

class _ResourceCard extends StatelessWidget {
  const _ResourceCard({
    required this.item,
    required this.selected,
    required this.onSelect,
    required this.controller,
  });
  final ResourceItem item;
  final bool selected;
  final VoidCallback onSelect;
  final AppController controller;
  @override
  Widget build(BuildContext context) {
    return ResourceContent(
      data: ResourceViewData(
        name: item.name,
        remark: item.remark,
        metadata: [
          if (item.protocol != null) item.protocol!.toUpperCase(),
          if (item.location.isNotEmpty) item.location,
        ].join(' · '),
        status: item.status,
        selected: selected,
        unavailable: item.eligible ? null : item.status,
      ),
      selectionKey: item.eligible
          ? ValueKey('select-${item.key.sourceId}')
          : null,
      detailsKey: ValueKey('details-${item.key.sourceId}'),
      onSelect: item.eligible ? onSelect : null,
      onDetails: () => showDialog<void>(
        context: context,
        builder: (context) => ListenableBuilder(
          listenable: controller,
          builder: (context, _) =>
              _ResourceDetails(item: item, controller: controller),
        ),
      ),
    );
  }
}

class _ResourceDetails extends StatelessWidget {
  const _ResourceDetails({required this.item, required this.controller});
  final ResourceItem item;
  final AppController controller;

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(item.name),
    content: SizedBox(
      width: 440,
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            DetailField('资源类型 / 编号', '${item.key.kind} / ${item.key.sourceId}'),
            if (item.remark.isNotEmpty) ...[
              const SizedBox(height: 18),
              DetailField('客户备注', item.remark),
            ],
            const SizedBox(height: 18),
            DetailField('状态', item.status),
            if (item.location.isNotEmpty) ...[
              const SizedBox(height: 18),
              DetailField('地区', item.location),
            ],
            if (item.protocol != null) ...[
              const SizedBox(height: 18),
              DetailField('协议', item.protocol!.toUpperCase()),
            ],
            const SizedBox(height: 18),
            DetailField(
              '节点出口 IP',
              controller.activeResource?.key == item.key
                  ? controller.observedExitIp ?? '暂未获取'
                  : '暂未获取',
            ),
            const SizedBox(height: 18),
            DetailField('客户到期时间', formatExpiry(item.expiresAtUtc)),
            if (item.protocol?.toLowerCase() == 'socks5') ...[
              const SizedBox(height: 18),
              const Text(
                'SOCKS5 本身不加密认证信息或代理传输。HTTPS 网站内容仍受其 TLS 保护；未核实的外层加密不作保证。',
              ),
            ],
            const SizedBox(height: 18),
            const Text('本页只读。购买、续费与业务变更在官网完成；返回后刷新资源状态。'),
            if (controller.websiteError != null) ...[
              const SizedBox(height: 12),
              NoticeCard(controller.websiteError!, error: true),
            ],
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: controller.openOfficialWebsite,
        child: const Text('前往官网'),
      ),
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('关闭'),
      ),
    ],
  );
}

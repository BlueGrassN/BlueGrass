import 'package:flutter/material.dart';

import 'entry_dns_policy.dart';
import 'ui/kit.dart';

class EntryDnsSettings extends StatelessWidget {
  const EntryDnsSettings({
    super.key,
    required this.value,
    required this.onSave,
  });
  final EntryDnsPolicy value;
  final Future<bool> Function(EntryDnsPolicy) onSave;

  @override
  Widget build(BuildContext context) => ListTile(
    leading: const Icon(Icons.dns_outlined),
    title: const Text('入口 DNS'),
    subtitle: Text(
      value.mode == 'auto' ? '自动' : '自定义 · ${value.candidates.length} 个 DNS',
    ),
    trailing: const Icon(Icons.chevron_right_rounded),
    onTap: () => showDialog<void>(
      context: context,
      builder: (_) => _EntryDnsDialog(initial: value, onSave: onSave),
    ),
  );
}

class _EntryDnsDialog extends StatefulWidget {
  const _EntryDnsDialog({required this.initial, required this.onSave});
  final EntryDnsPolicy initial;
  final Future<bool> Function(EntryDnsPolicy) onSave;
  @override
  State<_EntryDnsDialog> createState() => _EntryDnsDialogState();
}

class _DnsRow {
  _DnsRow([EntryDnsCandidate? value])
    : address = TextEditingController(text: value?.address ?? ''),
      port = TextEditingController(text: '${value?.port ?? 53}'),
      transport = value?.transport ?? 'udp';
  final TextEditingController address, port;
  String transport;
  void dispose() {
    address.dispose();
    port.dispose();
  }
}

class _EntryDnsDialogState extends State<_EntryDnsDialog> {
  late String mode = widget.initial.mode;
  late final rows = widget.initial.candidates.map(_DnsRow.new).toList();
  bool saving = false;
  String? error;
  @override
  void dispose() {
    for (final row in rows) {
      row.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    if (saving) return;
    EntryDnsPolicy policy;
    try {
      policy = mode == 'auto'
          ? const EntryDnsPolicy.auto()
          : EntryDnsPolicy.custom([
              for (final row in rows)
                EntryDnsCandidate(
                  address: row.address.text.trim(),
                  port: int.tryParse(row.port.text.trim()) ?? 0,
                  transport: row.transport,
                ),
            ]);
    } on FormatException catch (failure) {
      setState(() => error = failure.message);
      return;
    }
    setState(() {
      saving = true;
      error = null;
    });
    var accepted = false;
    try {
      accepted = await widget.onSave(policy);
    } catch (_) {
      /* Generic error only. */
    }
    if (!mounted) return;
    if (accepted) {
      Navigator.pop(context);
      return;
    }
    setState(() {
      saving = false;
      error = '入口 DNS 未能保存，原策略继续使用。请重试。';
    });
  }

  void _move(int from, int to) => setState(() {
    final row = rows.removeAt(from);
    rows.insert(to, row);
  });

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !saving,
    child: AlertDialog(
      title: const Text('入口 DNS 设置'),
      content: SurfaceScope(
        level: SurfaceLevel.floatingOf(Theme.of(context).colorScheme),
        // Sized by its rows up to the dialog's height, then scrolls.
        child: SizedBox(
          width: 560,
          child: SingleChildScrollView(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Text('只用于中转入口域名，不更改系统 DNS 或普通业务域名解析。保存后下次解析生效，不中断当前连接。'),
                const SizedBox(height: 16),
                SegmentedButton<String>(
                  showSelectedIcon: false,
                  expandedInsets: EdgeInsets.zero,
                  segments: const [
                    ButtonSegment(value: 'auto', label: Text('自动')),
                    ButtonSegment(value: 'custom', label: Text('自定义')),
                  ],
                  selected: {mode},
                  onSelectionChanged: saving
                      ? null
                      : (values) => setState(() {
                          mode = values.single;
                          error = null;
                          if (mode == 'custom' && rows.isEmpty) {
                            rows.add(_DnsRow());
                          }
                        }),
                ),
                const SizedBox(height: 12),
                Text(
                  mode == 'auto'
                      ? '优先使用物理网络 DNS，失败时尝试 Cloudflare（1.1.1.1）和阿里 DNS（223.5.5.5）；仅用于入口解析。'
                      : '仅在以下列表内解析和切换，不自动加入系统或其他公共 DNS。最多 8 项，靠前的优先。',
                ),
                if (mode == 'custom')
                  const Text(
                    '支持私有 IPv4 路由器、全局或 ULA IPv6；不支持回环、未指定、组播、'
                    'IPv4 映射 IPv6、IPv6 链路本地地址或区域标识。',
                  ),
                if (mode == 'custom') ...[
                  for (var i = 0; i < rows.length; i++) _row(i),
                  const SizedBox(height: 4),
                  TextButton.icon(
                    onPressed:
                        saving || rows.length >= EntryDnsPolicy.maxCandidates
                        ? null
                        : () => setState(() => rows.add(_DnsRow())),
                    icon: const Icon(Icons.add_rounded),
                    label: const Text('添加 DNS'),
                  ),
                ],
                if (error != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: Text(
                      error!,
                      style: AppTextStyle.footnote.of(
                        context,
                        emphasized: true,
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: saving ? null : () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: saving ? null : _save,
          child: Text(saving ? '正在保存' : '保存'),
        ),
      ],
    ),
  );

  Widget _row(int index) {
    final row = rows[index];
    final port = TextField(
      key: ValueKey('dns-port-$index'),
      controller: row.port,
      enabled: !saving,
      keyboardType: TextInputType.number,
      decoration: const InputDecoration(labelText: '端口'),
    );
    final transport = DropdownButtonFormField<String>(
      key: ValueKey('dns-transport-$index'),
      initialValue: row.transport,
      isExpanded: true,
      // Same text as the fields beside it, not the menu's title style.
      style: Theme.of(context).textTheme.bodyLarge
          ?.copyWith(color: Theme.of(context).colorScheme.onSurface),
      decoration: const InputDecoration(labelText: '传输'),
      items: const [
        DropdownMenuItem(value: 'udp', child: Text('UDP')),
        DropdownMenuItem(value: 'tcp', child: Text('TCP')),
      ],
      onChanged: saving
          ? null
          : (value) => setState(() => row.transport = value!),
    );
    final actions = [
      IconButton(
        tooltip: '上移 DNS ${index + 1}',
        onPressed: saving || index == 0 ? null : () => _move(index, index - 1),
        icon: const Icon(Icons.arrow_upward_rounded),
      ),
      IconButton(
        tooltip: '下移 DNS ${index + 1}',
        onPressed: saving || index == rows.length - 1
            ? null
            : () => _move(index, index + 1),
        icon: const Icon(Icons.arrow_downward_rounded),
      ),
      IconButton(
        tooltip: '删除 DNS ${index + 1}',
        onPressed: saving
            ? null
            : () => setState(() {
                rows.removeAt(index).dispose();
              }),
        icon: const Icon(Icons.delete_outline_rounded),
      ),
    ];
    return Padding(
      key: ObjectKey(row),
      padding: const EdgeInsets.only(top: 12),
      child: BgCard(
        radius: AppCorner.lg,
        padding: const EdgeInsets.fromLTRB(12, 10, 4, 10),
        child: LayoutBuilder(
          builder: (context, constraints) {
            // A narrow dialog moves the row's actions under its fields.
            final inline = constraints.maxWidth >= 440;
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: TextField(
                    key: ValueKey('dns-address-$index'),
                    controller: row.address,
                    enabled: !saving,
                    autocorrect: false,
                    decoration: InputDecoration(
                      labelText: 'DNS ${index + 1} · IPv4 或 IPv6',
                      hintText: '例如 1.1.1.1 或 2606:4700:4700::1111',
                    ),
                  ),
                ),
                const SizedBox(height: 8),
                if (inline)
                  Row(
                    children: [
                      Expanded(child: port),
                      const SizedBox(width: 8),
                      Expanded(child: transport),
                      ...actions,
                    ],
                  )
                else ...[
                  Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: Row(
                      children: [
                        Expanded(child: port),
                        const SizedBox(width: 8),
                        Expanded(child: transport),
                      ],
                    ),
                  ),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: actions,
                  ),
                ],
              ],
            );
          },
        ),
      ),
    );
  }
}

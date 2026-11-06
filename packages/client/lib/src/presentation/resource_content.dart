import 'package:flutter/material.dart';

import '../../ui/kit.dart';
import '../../ui/resource_kit.dart';

/// No credentials or endpoint configuration enter the resource presentation.
class ResourceViewData {
  const ResourceViewData({
    required this.name,
    required this.metadata,
    required this.selected,
    this.remark = '',
    this.countryCode,
    this.unavailable,
    this.status,
  });
  final String name, metadata, remark;
  final String? countryCode, unavailable, status;
  final bool selected;
}

/// One identity, eligibility, selection and detail presentation. The same
/// resource becomes a compact tile or a row according to the room it has.
class ResourceContent extends StatelessWidget {
  const ResourceContent({
    super.key,
    required this.data,
    required this.onSelect,
    required this.onDetails,
    this.selectionKey,
    this.detailsKey,
  });
  final ResourceViewData data;
  final VoidCallback? onSelect;
  final VoidCallback onDetails;
  final Key? selectionKey, detailsKey;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final secondary = AppTextStyle.footnote.of(
      context,
      color: colors.onSurfaceVariant,
    );
    final name = ResNameRemark(
      name: Text(
        data.name,
        style: AppTextStyle.subheadline.of(context, emphasized: true),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      remark: data.remark.isEmpty
          ? null
          : Tooltip(
              message: data.remark,
              child: Text(
                data.remark,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: secondary,
              ),
            ),
    );
    final details = IconButton(
      key: detailsKey,
      tooltip: '资源详情',
      constraints: const BoxConstraints(minWidth: 48, minHeight: 48),
      onPressed: onDetails,
      icon: const Icon(Icons.info_outline_rounded),
    );
    final metadata = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(data.metadata, style: secondary),
        if (data.unavailable != null)
          Text(
            data.unavailable!,
            style: secondary.copyWith(color: colors.error),
          ),
      ],
    );
    return Semantics(
      selected: data.selected,
      child: BgCard(
        radius: AppCorner.lg,
        padding: EdgeInsets.zero,
        child: LayoutBuilder(
          builder: (context, box) {
            if (box.maxWidth / MediaQuery.textScalerOf(context).scale(1) <
                520) {
              return ListTile(
                key: selectionKey,
                onTap: onSelect,
                contentPadding: const EdgeInsets.fromLTRB(14, 8, 10, 4),
                title: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Expanded(child: name),
                        if (data.selected) ...[
                          const SizedBox(width: 6),
                          Icon(
                            Icons.check_circle_rounded,
                            color: colors.primary,
                            size: 20,
                          ),
                        ],
                      ],
                    ),
                    const SizedBox(height: 6),
                    metadata,
                  ],
                ),
                subtitle: Row(
                  children: [
                    Expanded(
                      child: data.status != null
                          ? Text(data.status!, style: secondary)
                          : Align(
                              alignment: AlignmentDirectional.centerStart,
                              child: OutlinedButton(
                                onPressed: onSelect,
                                child: Text(
                                  data.unavailable != null
                                      ? '暂不可连接'
                                      : data.selected
                                      ? '已选择'
                                      : '选择',
                                ),
                              ),
                            ),
                    ),
                    details,
                  ],
                ),
              );
            }
            return ListTile(
              contentPadding: const EdgeInsetsDirectional.fromSTEB(
                16,
                2,
                12,
                2,
              ),
              minVerticalPadding: 12,
              horizontalTitleGap: 14,
              titleAlignment: ListTileTitleAlignment.center,
              leading: ResCountryBadge(
                code: data.countryCode ?? 'IP',
                selected: data.selected,
              ),
              title: Row(children: [Expanded(child: name)]),
              subtitle: Padding(
                padding: const EdgeInsets.only(top: 4),
                child: metadata,
              ),
              isThreeLine: true,
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                spacing: 4,
                children: [
                  details,
                  OutlinedButton(
                    key: selectionKey,
                    onPressed: onSelect,
                    style: ButtonStyle(
                      minimumSize: const WidgetStatePropertyAll(Size(84, 40)),
                      foregroundColor: data.selected && data.unavailable == null
                          ? WidgetStatePropertyAll(
                              colors.brightness == Brightness.dark
                                  ? colors.onPrimaryContainer
                                  : colors.primary,
                            )
                          : null,
                    ),
                    child: Text(
                      data.unavailable != null
                          ? '暂不可连接'
                          : data.selected
                          ? '已选择'
                          : '选择',
                    ),
                  ),
                ],
              ),
              onTap: onSelect ?? onDetails,
            );
          },
        ),
      ),
    );
  }
}

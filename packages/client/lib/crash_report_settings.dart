import 'dart:io';

import 'package:flutter/material.dart';

import 'crash_reports.dart';
import 'desktop_platform.dart';
import 'platform_store.dart';
import 'ui/kit.dart';
import 'ui/settings_rows.dart';

class CrashReportSettings extends StatefulWidget {
  const CrashReportSettings({
    super.key,
    required this.reports,
    this.exportFile,
  });
  final CrashReportCoordinator reports;
  final Future<String?> Function(String)? exportFile;
  @override
  State<CrashReportSettings> createState() => _CrashReportSettingsState();
}

class _CrashReportSettingsState extends State<CrashReportSettings> {
  bool saving = false;
  Future<void> _toggle(bool enabled) async {
    if (saving) return;
    if (enabled) {
      final allowed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('开启崩溃报告？'),
          content: Text(
            '仅记录开启后新发生的崩溃摘要：版本、${DesktopPlatform.current.name} 版本、模块、固定错误码、时间和白名单符号。'
            '不包含账号、密码、令牌、资源、访问记录、原始异常或完整日志，不收集使用行为。'
            '\n最多保存 20 条、7 天；关闭会取消后续发送并清除待发队列。已发送的报告无法从本机撤回。'
            '\n接收方：${widget.reports.receiverLabel}。'
            '${widget.reports.configured ? '开启后会自动发送新报告并有限重试。' : '当前只在本地暂存，不会上传。'}',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('同意并开启'),
            ),
          ],
        ),
      );
      if (!mounted || allowed != true) return;
    }
    setState(() => saving = true);
    await widget.reports.setEnabled(enabled);
    if (mounted) setState(() => saving = false);
  }

  Future<void> _preview() async {
    final value = await widget.reports.exportJson();
    if (!mounted) return;
    var exporting = false;
    String? output;
    await showDialog<void>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, update) => AlertDialog(
          title: const Text('预览崩溃摘要'),
          content: SurfaceScope(
            level: SurfaceLevel.floatingOf(Theme.of(context).colorScheme),
            child: SizedBox(
              width: 620,
              child: SingleChildScrollView(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  spacing: 12,
                  children: [
                    BgCard(
                      radius: AppCorner.lg,
                      padding: const EdgeInsets.all(14),
                      child: SelectableText(
                        value,
                        style: AppTextStyle.caption1
                            .of(context)
                            .copyWith(
                              fontFamily: DesktopPlatform.current.isMac
                                  ? 'Menlo'
                                  : 'Consolas',
                              fontFamilyFallback: [
                                DesktopPlatform.current.fontFamily,
                              ],
                              height: 1.5,
                            ),
                      ),
                    ),
                    if (output != null)
                      SelectableText(
                        output!,
                        style: AppTextStyle.footnote.of(
                          context,
                          color: Theme.of(context).colorScheme.onSurface,
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: exporting ? null : () => Navigator.pop(context),
              child: const Text('关闭'),
            ),
            FilledButton(
              onPressed: exporting
                  ? null
                  : () async {
                      update(() => exporting = true);
                      String? result;
                      try {
                        result = await (widget.exportFile ?? _exportFile)(
                          value,
                        );
                      } catch (_) {
                        result = DesktopPlatform.current.isMac
                            ? '导出失败，请检查所选位置的权限。'
                            : '导出失败，请检查当前用户临时目录权限。';
                      }
                      if (context.mounted) {
                        update(() {
                          output = result ?? '已取消导出。';
                          exporting = false;
                        });
                      }
                    },
              child: const Text('保存 JSON 文件'),
            ),
          ],
        ),
      ),
    );
  }

  static Future<String?> _exportFile(String value) async {
    if (DesktopPlatform.current.isMac) {
      return platform.invokeMethod<String>('saveFile', {
        'suggestedName': 'crash-reports.json',
        'content': value,
      });
    }
    // Explicit export only, to a fresh directory owned by the current user.
    // Never overwrite a preexisting file; the bounded sanitized preview is exact.
    final directory = await Directory.systemTemp.createTemp(
      'bluegrass-crash-export-',
    );
    final file = File(
      '${directory.path}${Platform.pathSeparator}crash-reports.json',
    );
    await file.writeAsString(value, flush: true);
    return file.path;
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.reports,
    builder: (context, _) {
      final reports = widget.reports;
      final s = Theme.of(context).colorScheme;
      return SettingsSection(
        title: '隐私',
        footer: '默认关闭。只采集崩溃，不采集使用行为。',
        children: [
          SwitchListTile(
            secondary: const Icon(Icons.bug_report_outlined),
            title: const Text('崩溃报告'),
            subtitle: Text(
              '${reports.enabled ? '已开启' : '已关闭'} · ${reports.receiverLabel}',
            ),
            value: reports.enabled,
            onChanged: saving || !reports.initialized ? null : _toggle,
          ),
          // The queue, its state and its two actions read as one row.
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              ListTile(
                leading: const Icon(Icons.inventory_2_outlined),
                title: const Text('本地待发摘要'),
                trailing: Text(
                  '${reports.count} 条',
                  style: AppTextStyle.subheadline.of(
                    context,
                    color: s.onSurfaceVariant,
                  ),
                ),
              ),
              if (reports.errorCode != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(
                    settingsTitleInset,
                    0,
                    16,
                    4,
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    spacing: 6,
                    children: [
                      Padding(
                        padding: const EdgeInsets.only(top: 1),
                        child: Icon(
                          Icons.error_outline_rounded,
                          size: 16,
                          color: s.error,
                        ),
                      ),
                      Expanded(
                        child: Text(
                          switch (reports.errorCode) {
                            'CONFIG_EMPTY' => '接收端未配置（CONFIG_EMPTY），上传不可用。',
                            'CRASH_STORAGE_INVALID' =>
                              '本地报告存储无法读取，已关闭采集；可重新选择开启。',
                            'CRASH_STORAGE_FAILED' => '报告设置或队列未能保存，请检查本地存储。',
                            'CRASH_SEND_FAILED' => '报告发送未完成，将在允许范围内重试；不影响连接。',
                            _ => '报告处理未完成。',
                          },
                          style: AppTextStyle.footnote.of(
                            context,
                            color: s.error,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              Padding(
                padding: const EdgeInsets.fromLTRB(
                  settingsTitleInset,
                  8,
                  16,
                  16,
                ),
                child: Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    OutlinedButton(
                      onPressed: reports.count == 0 ? null : _preview,
                      child: const Text('预览并导出'),
                    ),
                    OutlinedButton(
                      onPressed:
                          !reports.enabled ||
                              !reports.configured ||
                              reports.sending ||
                              reports.count == 0
                          ? null
                          : reports.sendPending,
                      child: Text(reports.sending ? '正在发送' : '发送报告'),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ],
      );
    },
  );
}

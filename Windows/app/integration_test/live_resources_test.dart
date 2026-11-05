import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/control_http_transport.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

const _environment = 'https://client-api.sunip.vip/api/client/v1';
const _pageSize = 2;
const _pageLimit = 100;

// Run only when the main integration/build owner coordinates Windows execution.
// BLUEGRASS_TEST_ACCOUNT_FILE follows live_connection_test's two-line format.
// Optional BLUEGRASS_CONTROL_HTTP_SMOKE_IP=192.140.166.247 selects physically
// bound GUI HTTPS. Without it, use ordinary direct IO. There is no automatic
// fallback, explicit proxy, native engine, exit binding, or expected-IP input.
void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'live API displays asset names and hides resources expired over 72 hours',
    (tester) async {
      const accountPath = String.fromEnvironment('BLUEGRASS_TEST_ACCOUNT_FILE');
      const controlIp = String.fromEnvironment(
        'BLUEGRASS_CONTROL_HTTP_SMOKE_IP',
      );
      _require(Platform.isWindows, 'windows_required');
      _require(accountPath.isNotEmpty, 'account_file_required');
      final credentials = <String>[];
      // Never expose filesystem exception details, account contents or paths.
      try {
        credentials.addAll(
          (await File(
            accountPath,
          ).readAsLines()).where((line) => line.trim().isNotEmpty),
        );
      } catch (_) {
        fail('account_file_unavailable');
      }
      if (credentials.length != 2) {
        credentials.clear();
        fail('account_file_format_invalid');
      }

      final store = WindowsSessionStore('integration_resources_${newId()}');
      ControlHttpTransport? physical;
      AppController? controller;
      var mounted = false;
      var stage = 'setup';
      final counts = <String, int>{};
      try {
        await store.clear();
        if (controlIp.isNotEmpty) {
          final address = InternetAddress.tryParse(controlIp);
          _require(
            address != null && address.type == InternetAddressType.IPv4,
            'numeric_ipv4_required',
          );
          physical = ControlHttpTransport(
            resolveHost: (host) async {
              _require(host == 'client-api.sunip.vip', 'control_host_mismatch');
              return [address!];
            },
          );
        }
        final source = ApiV2DataSource(
          baseUri: Uri.parse(_environment),
          store: store,
          transport: physical ?? IoHttpTransport(),
        );
        final device = newId();
        final c = AppController(
          source: source,
          getDeviceId: () async => device,
          readPreferences: () async => const Preferences(
            autoConnect: false,
            startWithWindows: false,
            startupPromptShown: true,
          ),
          savePreferences: (_) async {},
        );
        controller = c;
        _require(c.nativeFactory == null, 'native_engine_must_be_absent');
        mounted = true;
        await tester.pumpWidget(MyApp(controller: c));
        stage = 'initialize';
        await _waitUntil(tester, () => !c.initializing);
        _require(
          c.storageReady && !c.recoveryRequired && !c.active,
          'initialization_failed',
        );

        stage = 'login';
        await c.login(credentials[0].trim(), credentials[1]);
        credentials.clear();
        _require(c.customer != null && source.customer != null, 'login_failed');
        _require(!c.active, 'unexpected_connection');
        _require(c.message == null, 'initial_resource_sync_failed');

        stage = 'node_pages';
        final (nodes, nodePages) = await _allNodes(source);
        _require(nodes.isNotEmpty, 'account_has_no_nodes');
        counts['nodes'] = nodes.length;
        counts['node_pages'] = nodePages;

        // Controller refresh may already traverse ineligible/empty pages. Finish
        // any remaining cursors without selecting, qualifying or connecting.
        final controllerCursors = <String>{};
        for (var page = 0; c.hasMore && page < _pageLimit; page++) {
          final cursor = c.cursor;
          _require(
            cursor != null && controllerCursors.add(cursor),
            'controller_cursor_invalid',
          );
          await c.refresh(more: true);
          _require(c.message == null, 'controller_page_failed');
        }
        _require(!c.hasMore && !c.loading, 'controller_traversal_incomplete');
        final keys = nodes.map((node) => node.key).toSet();
        _require(
          c.nodes.length == nodes.length &&
              c.nodes.map((node) => node.key).toSet().containsAll(keys),
          'controller_node_membership_changed',
        );

        stage = 'capabilities_request';
        final queries = source as ResourceQueryDataSource;
        final capabilities = await queries.refreshCapabilities();
        stage = 'capabilities_available';
        _require(
          capabilities.connectableNodes && capabilities.resourceQueries,
          'resource_queries_unavailable',
        );
        stage = 'capabilities_authority';
        _require(
          !capabilities.allOwnedResources,
          'unexpected_authoritative_contract',
        );
        final owned = <String, List<OwnedResourceRecord>>{};
        for (final kind in ['subscription', 'asset']) {
          stage = '${kind}_pages';
          final (records, pages) = await _allOwned(queries, kind);
          owned[kind] = records;
          counts['${kind}_records'] = records.length;
          counts['${kind}_pages'] = pages;
        }

        stage = 'connection_list_ui';
        await tester.pumpAndSettle();
        final navigation = find.text('我的 IP');
        _require(
          navigation.evaluate().isNotEmpty,
          'resource_navigation_missing',
        );
        await tester.tap(navigation.last);
        await tester.pumpAndSettle();
        final connectionList = find.text('连接资源');
        _require(
          connectionList.evaluate().isNotEmpty,
          'connection_list_missing',
        );
        await tester.ensureVisible(connectionList.first);
        await tester.tap(connectionList.first);
        await tester.pumpAndSettle();

        final rows = find.byWidgetPredicate(
          _connectionRow,
          description: 'connection resource rows',
        );
        final visibleNodes = nodes
            .where(
              (node) => domain.isResourceVisible(
                node.expiresAtUtc,
                c.effectiveNowUtc,
              ),
            )
            .toList();
        _require(
          rows.evaluate().length == visibleNodes.length,
          'ui_node_count_mismatch',
        );
        final occurrences = <String, int>{};
        var visible = 0;
        var verifiedRemarks = 0;
        for (final node in visibleNodes) {
          // Match actual row titles without putting account/resource names into
          // Finder descriptions or failure output. Count duplicate names too.
          final ordinal = occurrences.update(
            node.name,
            (n) => n + 1,
            ifAbsent: () => 0,
          );
          final matching = find.ancestor(
            of: find.byWidgetPredicate(
              (widget) => widget is Text && widget.data == node.name,
              description: 'API connection asset title',
            ),
            matching: rows,
          );
          _require(matching.evaluate().length > ordinal, 'api_node_hidden');
          final row = matching.at(ordinal);
          await tester.ensureVisible(row);
          await tester.pumpAndSettle();
          _require(
            row.hitTestable().evaluate().length == 1,
            'api_node_not_visible_after_scroll',
          );
          {
            final buttons = tester
                .widgetList<OutlinedButton>(
                  find.descendant(
                    of: row,
                    matching: find.byType(OutlinedButton),
                  ),
                )
                .toList();
            _require(
              buttons.length == 1 &&
                  (buttons.single.onPressed != null) ==
                      (domain.prepareProfile(node, c.effectiveNowUtc)
                          is domain.Ready),
              'row_action_must_follow_config_eligibility',
            );
          }
          if (node.customerRemark.isNotEmpty) {
            _require(
              find
                      .descendant(
                        of: row,
                        matching: find.byWidgetPredicate(
                          (widget) =>
                              widget is Text &&
                              widget.data == node.customerRemark,
                          description: 'API inline customer remark',
                        ),
                      )
                      .evaluate()
                      .length ==
                  1,
              'inline_customer_remark_missing',
            );
            stage = 'customer_remark_detail';
            final detailsButton = find.descendant(
              of: row,
              matching: find.byTooltip('资源详情'),
            );
            await tester.tap(detailsButton);
            await tester.pumpAndSettle();
            final dialog = tester.widget<AlertDialog>(find.byType(AlertDialog));
            _require(
              dialog.title is Text && (dialog.title! as Text).data == node.name,
              'asset_detail_title_mismatch',
            );
            _require(
              find
                      .byWidgetPredicate(
                        (w) =>
                            w is SelectableText &&
                            w.data == node.customerRemark,
                        description: 'separate customer remark',
                      )
                      .evaluate()
                      .length ==
                  1,
              'customer_remark_not_separate',
            );
            await tester.tap(find.text('关闭'));
            await tester.pumpAndSettle();
            verifiedRemarks++;
            stage = 'connection_list_ui';
          }
          visible++;
        }
        counts['ui_visible_nodes'] = visible;
        counts['ui_verified_customer_remarks'] = verifiedRemarks;
        stage = 'owned_list_ui';
        await tester.ensureVisible(find.text('全部资源'));
        await tester.tap(find.text('全部资源'));
        for (final kind in ['subscription', 'asset']) {
          stage = '${kind}_visibility_ui';
          if (kind == 'asset') {
            await tester.ensureVisible(find.text('资产').last);
            await tester.tap(find.text('资产').last);
          }
          await tester.pump();
          await _waitUntil(
            tester,
            () => find.text('正在查询资源…').evaluate().isEmpty,
          );
          final more = find.text(kind == 'subscription' ? '加载更多订阅' : '加载更多资产');
          for (
            var page = 0;
            more.evaluate().isNotEmpty && page < _pageLimit;
            page++
          ) {
            await tester.ensureVisible(more);
            await tester.tap(more);
            await tester.pump();
            await _waitUntil(
              tester,
              () => find.text('正在查询资源…').evaluate().isEmpty,
            );
          }
          _require(more.evaluate().isEmpty, 'owned_ui_traversal_incomplete');
          final shown = owned[kind]!
              .where(
                (r) => domain.isResourceVisible(
                  r.summary.expiresAtUtc,
                  c.effectiveNowUtc,
                ),
              )
              .toList();
          final expectedNames = shown.map((r) => r.summary.name).toList()
            ..sort();
          final actualNames =
              tester
                  .widgetList<ListTile>(
                    find.byWidgetPredicate(
                      (w) =>
                          w is ListTile &&
                          w.isThreeLine == true &&
                          w.title is Text &&
                          w.trailing is Icon,
                    ),
                  )
                  .map((w) => (w.title! as Text).data!)
                  .toList()
                ..sort();
          _require(
            actualNames.length == expectedNames.length &&
                List.generate(
                  actualNames.length,
                  (i) => actualNames[i] == expectedNames[i],
                ).every((v) => v),
            'owned_visible_asset_names_mismatch',
          );
          counts['ui_visible_$kind'] = shown.length;
          counts['ui_hidden_expired_$kind'] =
              owned[kind]!.length - shown.length;
        }
        _require(
          !c.active && c.actualMode == 'none' && c.localProxyAddress == null,
          'unexpected_native_session',
        );
      } on _LiveCheckFailure catch (error) {
        fail('live_resources_failed_at_${stage}_${error.code}');
      } on ApiFailure catch (error) {
        const codes = {
          'invalid_response',
          'network_error',
          'timeout',
          'rate_limited',
          'retry_later',
          'session_changed',
          'not_found',
          'capability_unavailable',
        };
        fail(
          'live_resources_failed_at_${stage}_${codes.contains(error.code) ? error.code : "api_error"}',
        );
      } catch (_) {
        // Do not print nested errors, widgets, resources, credentials or tokens.
        fail('live_resources_failed_at_$stage');
      } finally {
        credentials.clear();
        var cleanupFailed = false;
        try {
          if (controller != null) {
            await controller.logout();
            cleanupFailed =
                controller.customer != null || controller.message != null;
          }
        } catch (_) {
          cleanupFailed = true;
        }
        try {
          await store.clear();
          cleanupFailed = (await store.read()) != null || cleanupFailed;
        } catch (_) {
          cleanupFailed = true;
        } finally {
          try {
            if (mounted) await tester.pumpWidget(const SizedBox.shrink());
          } finally {
            controller?.dispose();
            physical?.dispose();
          }
        }
        _require(!cleanupFailed, 'live_resources_cleanup_unconfirmed');
      }
      // Only allowlisted integer counts leave this test. Empty asset traversal
      // is valid; terminal cursors do not establish global ownership completeness.
      binding.reportData = {'counts': counts};
      debugPrint('live_resources_counts ${jsonEncode(counts)}');
    },
    timeout: const Timeout(Duration(minutes: 4)),
  );
}

bool _connectionRow(Widget widget) =>
    widget is ListTile &&
    widget.isThreeLine == true &&
    widget.title is Row &&
    widget.trailing is Row;

class _LiveCheckFailure implements Exception {
  const _LiveCheckFailure(this.code);
  final String code;
}

void _require(bool condition, String safeCode) {
  if (!condition) throw _LiveCheckFailure(safeCode);
}

Future<void> _waitUntil(WidgetTester tester, bool Function() ready) async {
  final elapsed = Stopwatch()..start();
  while (!ready() && elapsed.elapsed < const Duration(seconds: 20)) {
    await Future<void>.delayed(const Duration(milliseconds: 50));
    await tester.pump();
  }
  _require(ready(), 'initialization_timeout');
}

Future<(List<domain.NodeRecord>, int)> _allNodes(ApiV2DataSource source) async {
  final nodes = <domain.NodeRecord>[];
  final keys = <domain.ResourceKey>{};
  final cursors = <String>{};
  String? cursor;
  for (var count = 1; count <= _pageLimit; count++) {
    final page = await source.listNodes(cursor: cursor, perPage: _pageSize);
    for (final node in page.items) {
      _require(keys.add(node.key), 'node_traversal_changed');
      nodes.add(node);
    }
    if (!page.hasMore) {
      _require(page.nextCursor == null, 'node_terminal_cursor_invalid');
      return (nodes, count);
    }
    final next = page.nextCursor;
    _require(
      next != null && next.isNotEmpty && cursors.add(next),
      'node_cursor_invalid',
    );
    cursor = next;
  }
  fail('node_traversal_limit');
}

Future<(List<OwnedResourceRecord>, int)> _allOwned(
  ResourceQueryDataSource source,
  String kind,
) async {
  final keys = <domain.ResourceKey>{};
  final cursors = <String>{};
  String? cursor;
  final records = <OwnedResourceRecord>[];
  for (var count = 1; count <= _pageLimit; count++) {
    final page = await source.listResources(
      kind: kind,
      cursor: cursor,
      perPage: _pageSize,
    );
    _require(
      page.kind == kind &&
          page.ownershipCoverage == 'conservative' &&
          page.consistency == 'per_page' &&
          !page.snapshotComplete,
      'owned_resource_coverage_invalid',
    );
    for (final resource in page.items) {
      _require(
        resource.summary.owned &&
            resource.summary.key.kind == kind &&
            keys.add(resource.summary.key),
        'owned_resource_traversal_changed',
      );
      records.add(resource);
    }
    if (!page.hasMore) {
      _require(page.nextCursor == null, 'owned_terminal_cursor_invalid');
      return (records, count);
    }
    final next = page.nextCursor;
    _require(
      next != null && next.isNotEmpty && cursors.add(next),
      'owned_cursor_invalid',
    );
    cursor = next;
  }
  fail('owned_traversal_limit');
}

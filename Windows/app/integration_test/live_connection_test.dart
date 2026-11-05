import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/control_http_transport.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'utils/windows_proxy_snapshot.dart';

// Executed only by the main owner's coordinated live Windows run.
// No binding file, expected IP, fixed resource ID, or external proxy mutation.
void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('all API nodes connect sequentially and display observed exits', (
    tester,
  ) async {
    const accountPath = String.fromEnvironment('BLUEGRASS_TEST_ACCOUNT_FILE');
    const proxyUrl = String.fromEnvironment('BLUEGRASS_API_PROXY');
    const controlIp = String.fromEnvironment('BLUEGRASS_CONTROL_HTTP_SMOKE_IP');
    _require(
      Platform.isWindows && accountPath.isNotEmpty,
      'explicit_windows_test_inputs_required',
    );
    final credentials = <String>[];
    try {
      credentials.addAll(
        (await File(
          accountPath,
        ).readAsLines()).where((line) => line.trim().isNotEmpty),
      );
    } catch (_) {
      fail('account_file_unavailable');
    }
    _require(credentials.length == 2, 'account_file_format_invalid');
    final store = WindowsSessionStore('integration_connections_${newId()}');
    final device = newId();
    ControlHttpTransport? physical;
    AppController? controller;
    WindowsProxySnapshot? original;
    var mounted = false;
    var stage = 'setup';
    const capture = Key('live-connected-capture');
    final observations = <Map<String, Object?>>[];
    final counts = <String, int>{
      'nodes': 0,
      'connected': 0,
      'https_observed': 0,
      'display_observed': 0,
      'display_unknown': 0,
      'https_display_matches': 0,
      'https_display_changes': 0,
      'restored': 0,
    };
    try {
      original = readWindowsProxySnapshot();
      await store.clear();
      if (controlIp.isNotEmpty) {
        final address = InternetAddress.tryParse(controlIp);
        _require(
          address != null && address.type == InternetAddressType.IPv4,
          'numeric_control_ipv4_required',
        );
        physical = ControlHttpTransport(
          resolveHost: (host) async {
            _require(host == Uri.parse(apiUrl).host, 'control_host_mismatch');
            return [address!];
          },
        );
      }
      final source = ApiV2DataSource(
        baseUri: Uri.parse(apiUrl),
        store: store,
        transport:
            physical ??
            IoHttpTransport(
              explicitProxy: proxyUrl.isEmpty ? null : Uri.parse(proxyUrl),
            ),
      );
      final c = AppController(
        source: source,
        getDeviceId: () async => device,
        readPreferences: () async => const Preferences(
          autoConnect: false,
          startWithWindows: false,
          startupPromptShown: true,
          socksWarningShown: true,
        ),
        savePreferences: (_) async {},
        nativeFactory: NativeClient.new,
      );
      controller = c;
      mounted = true;
      await tester.pumpWidget(
        RepaintBoundary(
          key: capture,
          child: MyApp(controller: c),
        ),
      );
      stage = 'initialize';
      await _waitUntil(
        tester,
        () => !c.initializing,
        const Duration(seconds: 30),
      );
      _require(
        !c.recoveryRequired && !c.active,
        'startup_recovery_unconfirmed',
      );
      _require(
        readWindowsProxySnapshot().sameAs(original),
        'startup_proxy_restoration_mismatch',
      );
      stage = 'login';
      await c.login(credentials[0].trim(), credentials[1]);
      credentials.clear();
      _require(
        c.customer != null && c.message == null,
        'api_login_or_sync_failed',
      );
      final cursors = <String>{};
      for (var page = 0; c.hasMore && page < 100; page++) {
        final cursor = c.cursor;
        _require(cursor != null && cursors.add(cursor), 'node_cursor_invalid');
        await c.refresh(more: true);
        _require(c.message == null, 'node_page_failed');
      }
      _require(!c.hasMore && c.nodes.isNotEmpty, 'node_traversal_incomplete');
      final nodes = List<domain.NodeRecord>.of(c.nodes);
      _require(
        nodes.map((node) => node.key).toSet().length == nodes.length,
        'duplicate_node_identity',
      );
      counts['nodes'] = nodes.length;
      for (var index = 0; index < nodes.length; index++) {
        stage = 'node_${index}_connect';
        _require(
          readWindowsProxySnapshot().sameAs(original),
          'prior_proxy_restoration_mismatch',
        );
        await c.select(nodes[index]);
        await c.connect();
        _require(
          c.connected && c.selected?.key == nodes[index].key,
          'node_connection_failed',
        );
        final address = c.localProxyAddress;
        _require(address == '127.0.0.1:12808', 'private_proxy_address_invalid');
        counts['connected'] = counts['connected']! + 1;
        stage = 'node_${index}_observe';
        final observed = await _observeThroughProxy(address!);
        if (observed != null) {
          counts['https_observed'] = counts['https_observed']! + 1;
        }
        // The existing session-bound diagnosis retries observation; its failure
        // must not pause/disconnect otherwise healthy forwarding.
        await c.diagnose();
        _require(c.connected, 'advisory_diagnosis_changed_connection');
        await tester.pumpAndSettle();
        _require(
          find.text('系统代理已连接').evaluate().length == 1,
          'connected_state_not_displayed',
        );
        _require(
          find.text('127.0.0.1:12808').evaluate().length == 1,
          'fixed_proxy_address_not_displayed',
        );
        final actual = c.actualExitIp;
        if (actual == null) {
          _require(
            find.text('暂未获取').evaluate().length == 1,
            'unknown_observation_not_displayed',
          );
          counts['display_unknown'] = counts['display_unknown']! + 1;
        } else {
          _require(
            domain.isPublicIpv4(actual),
            'invalid_observation_displayed',
          );
          _require(
            find.text(actual).evaluate().length == 1,
            'observed_ip_not_displayed',
          );
          counts['display_observed'] = counts['display_observed']! + 1;
          // A live cross-check is evidence, not a connection eligibility gate.
          // A rotating relay may legitimately differ between HTTPS requests.
          if (observed == actual) {
            counts['https_display_matches'] =
                counts['https_display_matches']! + 1;
          } else if (observed != null) {
            counts['https_display_changes'] =
                counts['https_display_changes']! + 1;
          }
        }
        final evidence = <String, Object?>{
          'resource_id': nodes[index].key.sourceId,
          'observed_ip': observed,
          'displayed_ip': actual,
          'connected': c.connected,
          'local_proxy_address': address,
          'screenshot': null,
          'restored': false,
        };
        observations.add(evidence);
        stage = 'node_${index}_screenshot';
        evidence['screenshot'] = await _connectedScreenshot(
          tester,
          capture,
          index,
        );
        stage = 'node_${index}_disconnect';
        _require(await c.disconnect(), 'disconnect_unconfirmed');
        _require(
          !c.active && c.actualExitIp == null && c.localProxyAddress == null,
          'disconnected_state_invalid',
        );
        _require(
          readWindowsProxySnapshot().sameAs(original),
          'exact_proxy_restoration_mismatch',
        );
        counts['restored'] = counts['restored']! + 1;
        evidence['restored'] = true;
      }
      _require(
        counts['connected'] == counts['nodes'] &&
            counts['restored'] == counts['nodes'],
        'all_nodes_not_exercised',
      );
      stage = 'all_nodes_observation_evidence';
      // Unknown observation remains a valid product connection. A live
      // acceptance run must nevertheless prove both HTTPS observation and UI
      // display for every node; rotating public exits need not be equal.
      _require(
        counts['https_observed'] == counts['nodes'] &&
            counts['display_observed'] == counts['nodes'],
        'observation_evidence_incomplete',
      );
    } catch (_) {
      // Never print resource profiles, credentials, API tokens, proxy settings,
      // account paths, or the underlying arbitrary exception.
      fail('live_connections_failed_at_$stage');
    } finally {
      credentials.clear();
      var cleaned = true;
      try {
        if (controller != null) {
          cleaned = await controller.disconnect();
          if (cleaned) await controller.logout();
          cleaned = cleaned && controller.customer == null;
        }
        await store.clear();
        cleaned = cleaned && (await store.read()) == null;
        if (original != null) {
          cleaned = readWindowsProxySnapshot().sameAs(original) && cleaned;
        }
      } catch (_) {
        cleaned = false;
      }
      try {
        if (mounted) await tester.pumpWidget(const SizedBox.shrink());
      } finally {
        controller?.dispose();
        physical?.dispose();
      }
      binding.reportData = {'counts': counts, 'observations': observations};
      debugPrint('live_connections_evidence ${jsonEncode(binding.reportData)}');
      _require(cleaned, 'live_connections_cleanup_unconfirmed');
    }
  }, timeout: const Timeout(Duration(minutes: 15)));
}

Future<String?> _connectedScreenshot(
  WidgetTester tester,
  Key capture,
  int index,
) async {
  const output = String.fromEnvironment('BLUEGRASS_SCREENSHOTS');
  if (output.isEmpty) return null;
  final boundary = tester.renderObject<RenderRepaintBoundary>(
    find.byKey(capture),
  );
  final image = await boundary.toImage(pixelRatio: 1);
  try {
    final png = await image.toByteData(format: ui.ImageByteFormat.png);
    if (png == null) throw StateError('screenshot_unavailable');
    // An ordinal filename prevents an API resource ID from becoming a path.
    final filename = 'live-connected-$index.png';
    await Directory(output).create(recursive: true);
    await File('$output/$filename').writeAsBytes(png.buffer.asUint8List());
    return filename;
  } finally {
    image.dispose();
  }
}

void _require(bool condition, String code) {
  if (!condition) fail(code);
}

Future<void> _waitUntil(
  WidgetTester tester,
  bool Function() ready,
  Duration limit,
) async {
  final clock = Stopwatch()..start();
  while (!ready() && clock.elapsed < limit) {
    await Future<void>.delayed(const Duration(milliseconds: 50));
    await tester.pump();
  }
  _require(ready(), 'startup_timeout');
}

Future<String?> _observeThroughProxy(String address) async {
  final http = HttpClient();
  http.findProxy = (_) => 'PROXY $address';
  http.connectionTimeout = const Duration(seconds: 15);
  try {
    return await (() async {
      final request = await http.getUrl(
        Uri.parse('https://api.ipify.org?format=json'),
      );
      request.followRedirects = false;
      final response = await request.close();
      if (response.statusCode != 200) return null;
      final bytes = <int>[];
      await for (final chunk in response) {
        if (bytes.length + chunk.length > 4096) return null;
        bytes.addAll(chunk);
      }
      final decoded = jsonDecode(utf8.decode(bytes));
      final value = decoded is Map ? decoded['ip'] : null;
      return value is String && domain.isPublicIpv4(value) ? value : null;
    })().timeout(const Duration(seconds: 25));
  } catch (_) {
    return null;
  } finally {
    http.close(force: true);
  }
}

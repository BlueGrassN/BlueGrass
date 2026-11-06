import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_client/crash_reports.dart';
import 'package:flutter_test/flutter_test.dart';

class MemoryCrashStore implements CrashReportStore {
  String? value;
  Completer<void>? writeGate;
  bool failWrites = false;
  @override
  Future<String?> read() async => value;
  @override
  Future<void> write(String data) async {
    await writeGate?.future;
    if (failWrites) throw StateError('storage unavailable');
    value = data;
  }

  @override
  Future<void> delete() async {
    value = null;
  }
}

class FakeCrashTransport implements CrashReportTransport {
  @override
  Uri? get receiver => Uri.parse('https://receiver.invalid/crash');
  final sent = <Map<String, Object?>>[];
  int cancels = 0;
  bool accepted = false;
  Completer<bool>? pending;
  @override
  Future<bool> send(Map<String, Object?> report) async {
    sent.add(report);
    return pending == null ? accepted : pending!.future;
  }

  @override
  void cancel() {
    cancels++;
  }
}

const signal = CrashSignal(
  module: CrashModule.ui,
  code: CrashCode.uiUnhandled,
  frames: [CrashFrame.uiDispatch],
);

void main() {
  late MemoryCrashStore store;
  late CrashReportCoordinator reports;
  var now = DateTime.utc(2026, 10, 12);
  var sequence = 0;
  CrashReportCoordinator create({CrashReportTransport? transport}) =>
      CrashReportCoordinator(
        store: store,
        transport: transport,
        now: () => now,
        nextId: () => (++sequence).toRadixString(16).padLeft(32, '0'),
      );
  setUp(() {
    store = MemoryCrashStore();
    now = DateTime.utc(2026, 10, 12);
    sequence = 0;
    reports = create();
  });
  tearDown(() {
    reports.dispose();
  });
  test('default off creates no queue; enable records only new events and persists consent', () async {
    await reports.initialize();
    await reports.record(signal);
    expect(store.value, isNull);
    expect(reports.count, 0);
    expect(await reports.setEnabled(true), isTrue);
    await reports.record(signal);
    expect(reports.count, 1);
    reports.dispose();
    reports = create();
    await reports.initialize();
    expect(reports.enabled, isTrue);
    expect(reports.count, 1);
    await reports.sendPending();
    expect(reports.errorCode, 'CONFIG_EMPTY');
  });
  test('beta build version remains valid across crash queue reload', () async {
    await reports.initialize();
    await reports.setEnabled(true);
    await reports.record(signal);
    final value = jsonDecode(store.value!);
    value['queue'][0]['report']['appVersion'] = '0.1.0-beta.1+2';
    store.value = jsonEncode(value);
    reports.dispose();
    reports = create();
    await reports.initialize();
    expect(reports.enabled, isTrue);
    expect(reports.count, 1);
  });
  test(
    'Xray reports preserve queued reports from the previous engine on reload',
    () async {
      await reports.initialize();
      await reports.setEnabled(true);
      await reports.record(signal);
      final value = jsonDecode(store.value!);
      value['queue'][0]['report']['coreVersion'] = 'sing-box/1.14.2';
      store.value = jsonEncode(value);
      reports.dispose();
      reports = create();
      await reports.initialize();
      expect(reports.enabled, isTrue);
      expect(reports.count, 1);
      await reports.record(signal);
      reports.dispose();
      reports = create();
      await reports.initialize();
      final exported = jsonDecode(await reports.exportJson())['reports'] as List;
      expect(exported, hasLength(2));
      expect(
        exported.map((report) => report['coreVersion']),
        ['sing-box/1.14.2', 'xray/26.10.17'],
      );
    },
  );
  test(
    'strict schema bounded queue and age; export contains only whitelist',
    () async {
      await reports.initialize();
      await reports.setEnabled(true);
      for (var i = 0; i < 25; i++) {
        await reports.record(signal);
      }
      expect(reports.count, 20);
      final exported =
          jsonDecode(await reports.exportJson()) as Map<String, dynamic>;
      final report =
          (exported['reports'] as List).first as Map<String, dynamic>;
      expect(report.keys.toSet(), {
        'schema',
        'reportId',
        'appVersion',
        'coreVersion',
        'platform',
        'osVersion',
        'module',
        'code',
        'frames',
        'reportedAtUtc',
      });
      expect(report['code'], 'UI_UNHANDLED');
      expect(report['frames'], ['ui.dispatch']);
      expect(utf8.encode(jsonEncode(report)).length, lessThanOrEqualTo(2048));
      now = now.add(const Duration(days: 8));
      expect(
        (jsonDecode(await reports.exportJson())['reports'] as List),
        isEmpty,
      );
    },
  );
  test(
    'corrupt/expanded persisted schema fails closed and never exports it',
    () async {
      await reports.initialize();
      await reports.setEnabled(true);
      await reports.record(signal);
      final value = jsonDecode(store.value!);
      value['queue'][0]['report']['extra'] = 'not allowed';
      store.value = jsonEncode(value);
      reports.dispose();
      reports = create();
      await reports.initialize();
      expect(reports.enabled, isFalse);
      expect(reports.count, 0);
      expect(reports.errorCode, 'CRASH_STORAGE_INVALID');
      expect(
        (jsonDecode(await reports.exportJson())['reports'] as List),
        isEmpty,
      );
    },
  );
  test('turning off cancels in-flight work, clears durable queue, rejects late events', () async {
    final transport = FakeCrashTransport()..pending = Completer<bool>();
    reports.dispose();
    reports = create(transport: transport);
    await reports.initialize();
    await reports.setEnabled(true);
    await reports.record(signal);
    await Future<void>.delayed(Duration.zero);
    expect(transport.sent, hasLength(1));
    await reports.setEnabled(false);
    expect(transport.cancels, greaterThan(0));
    transport.pending!.complete(true);
    await Future<void>.delayed(Duration.zero);
    await reports.record(signal);
    expect(reports.count, 0);
    expect(jsonDecode(store.value!)['enabled'], false);
    expect(jsonDecode(store.value!)['queue'], isEmpty);
  });
  test(
    'bounded retry persists attempts and does not retry beyond three sends',
    () async {
      final transport = FakeCrashTransport();
      reports.dispose();
      reports = create(transport: transport);
      await reports.initialize();
      await reports.setEnabled(true);
      await reports.record(signal);
      await Future<void>.delayed(Duration.zero);
      for (var i = 0; i < 5; i++) {
        now = now.add(const Duration(hours: 1));
        await reports.sendPending();
      }
      expect(transport.sent, hasLength(3));
      reports.dispose();
      reports = create(transport: transport);
      await reports.initialize();
      expect(transport.sent, hasLength(3));
    },
  );
  test(
    'accepted reports removed; disabling remains off if preference write fails',
    () async {
      final transport = FakeCrashTransport()..accepted = true;
      reports.dispose();
      reports = create(transport: transport);
      await reports.initialize();
      await reports.setEnabled(true);
      await reports.record(signal);
      await Future<void>.delayed(Duration.zero);
      expect(reports.count, 0);
      store.failWrites = true;
      expect(
        await reports.setEnabled(false),
        isTrue,
      ); // exact owned record delete fallback
      expect(store.value, isNull);
      expect(reports.enabled, isFalse);
    },
  );
  test('late enable persistence cannot undo a newer revocation', () async {
    await reports.initialize();
    store.writeGate = Completer<void>();
    final enabling = reports.setEnabled(true);
    await Future<void>.delayed(Duration.zero);
    final disabling = reports.setEnabled(false);
    var observedEnabledAfterRevocation = false;
    reports.addListener(() {
      if (reports.enabled) observedEnabledAfterRevocation = true;
    });
    store.writeGate!.complete();
    await enabling;
    await disabling;
    expect(observedEnabledAfterRevocation, isFalse);
    expect(reports.enabled, isFalse);
  });
  testWidgets(
    'retention expires while idle even without a configured receiver',
    (tester) async {
      // Construct the serialization Future inside this test's FakeAsync zone.
      reports.dispose();
      reports = create();
      await reports.initialize();
      await reports.setEnabled(true);
      await reports.record(signal);
      expect(reports.count, 1);
      now = now.add(const Duration(days: 7));
      await tester.pump(const Duration(days: 7));
      await tester.pump();
      expect(reports.count, 0);
      expect(jsonDecode(store.value!)['queue'], isEmpty);
      reports.dispose();
    },
  );
}

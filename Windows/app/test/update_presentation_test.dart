import 'dart:async';

import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:flutter_test/flutter_test.dart';

const candidate =
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const available = UpdateSnapshot(
  state: UpdateState.available,
  candidateId: candidate,
  version: '0.2.0',
  build: 2,
  size: 100,
  remaining: 100,
);
const ready = UpdateSnapshot(
  state: UpdateState.ready,
  candidateId: candidate,
  version: '0.2.0',
  build: 2,
  size: 100,
  downloaded: 100,
  remaining: 0,
);

class FakeUpdateClient extends UpdateClient {
  final events = StreamController<UpdateSnapshot>.broadcast();
  final calls = <String>[];
  UpdateSnapshot checked = available;
  String? checkFailure, downloadFailure, launchFailure;
  Completer<UpdateSnapshot>? downloadResult;
  @override
  Stream<UpdateSnapshot> get progress => events.stream;
  @override
  Future<UpdateSnapshot> check() async {
    calls.add('check');
    if (checkFailure != null) throw UpdateException(checkFailure!);
    return checked;
  }

  @override
  Future<UpdateSnapshot> download(
    String id, {
    bool allowMetered = false,
  }) async {
    calls.add('download:$id:$allowMetered');
    if (downloadFailure != null) throw UpdateException(downloadFailure!);
    return downloadResult == null ? ready : downloadResult!.future;
  }

  @override
  Future<UpdateSnapshot> launch(
    String id, {
    required bool installConsent,
  }) async {
    calls.add('launch:$id:$installConsent');
    if (launchFailure != null) throw UpdateException(launchFailure!);
    return const UpdateSnapshot(
      state: UpdateState.launchRequested,
      candidateId: candidate,
      version: '0.2.0',
      build: 2,
      size: 100,
      downloaded: 100,
    );
  }

  @override
  void cancel() {
    calls.add('cancel');
    downloadResult?.completeError(const UpdateException('UPDATE_CANCELLED'));
  }

  @override
  Future<void> dispose() async {
    await events.close();
  }
}

void main() {
  late FakeUpdateClient client;
  late UpdatePresentation ui;
  var pauseResult = true;
  setUp(() {
    client = FakeUpdateClient();
    pauseResult = true;
    ui = UpdatePresentation(
      client: client,
      pauseBeforeInstall: () async {
        client.calls.add('pause');
        return pauseResult;
      },
      handoffAfterLaunch: () async {
        client.calls.add('handoff');
        return true;
      },
      exitAfterHandoff: () async {
        client.calls.add('exit');
      },
    );
  });
  tearDown(() async {
    ui.dispose();
    await client.dispose();
  });

  test('construction is passive and check downloads eligible package without installing', () async {
    expect(client.calls, isEmpty);
    await ui.check();
    expect(client.calls, ['check', 'download:$candidate:false']);
    expect(ui.snapshot, ready);
    expect(ui.readyNoticeVisible, isTrue);
    client.checkFailure = 'UPDATE_UNCONFIGURED';
    await ui.check();
    expect(ui.errorCode, 'UPDATE_UNCONFIGURED');
    expect(ui.snapshot, isNull);
    expect(ui.statusText, contains('未配置'));
  });
  test(
    'cached readiness is nonmodal and dismissal preserves settings action',
    () async {
      client.checked = ready;
      await ui.check();
      expect(ui.readyNoticeVisible, isTrue);
      ui.dismissReadyNotice();
      expect(ui.readyNoticeVisible, isFalse);
      expect(ui.canInstall, isTrue);
      await ui.check();
      expect(ui.readyNoticeVisible, isFalse);
      expect(client.calls, ['check', 'check']);
    },
  );
  test(
    'metered consent is explicit and applies to this one download',
    () async {
      client.downloadFailure = 'UPDATE_WAITING_NETWORK';
      await ui.check();
      expect(ui.waitingForNetworkConsent, isTrue);
      expect(client.calls.last, 'download:$candidate:false');
      client.downloadFailure = null;
      await ui.download(allowMetered: true);
      expect(client.calls.last, 'download:$candidate:true');
      expect(ui.readyNoticeVisible, isTrue);
      expect(
        client.calls.any((c) => c == 'pause' || c.startsWith('launch:')),
        isFalse,
      );
    },
  );
  test('cancel is download-only and preserves manual resumption', () async {
    client.downloadFailure = 'UPDATE_WAITING_NETWORK';
    await ui.check();
    await ui.cancelDownload();
    expect(client.calls, ['check', 'download:$candidate:false']);
    client.downloadFailure = null;
    client.downloadResult = Completer();
    final downloading = ui.download();
    await Future<void>.delayed(Duration.zero);
    client.events.add(
      const UpdateSnapshot(
        state: UpdateState.downloading,
        candidateId: candidate,
        version: '0.2.0',
        build: 2,
        size: 100,
        downloaded: 30,
        remaining: 70,
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(ui.snapshot!.downloaded, 30);
    await ui.cancelDownload();
    await downloading;
    expect(ui.busy, isFalse);
    expect(ui.canDownload, isTrue);
    expect(ui.errorCode, 'UPDATE_CANCELLED');
  });
  test(
    'install requires confirmation then verified pause before launch',
    () async {
      client.checked = ready;
      await ui.check();
      await ui.install(confirmed: false);
      expect(client.calls, ['check']);
      pauseResult = false;
      await ui.install(confirmed: true);
      expect(client.calls, ['check', 'pause']);
      expect(ui.errorCode, 'UPDATE_PAUSE_UNCONFIRMED');
      pauseResult = true;
      await ui.install(confirmed: true);
      expect(client.calls, [
        'check',
        'pause',
        'pause',
        'launch:$candidate:true',
        'handoff',
        'exit',
      ]);
      expect(ui.snapshot!.state, UpdateState.launchRequested);
      expect(ui.statusText, contains('尚未确认安装完成'));
    },
  );
  test(
    'uncertain elevation outcome never retries or claims install success',
    () async {
      client.checked = ready;
      client.launchFailure = 'UPDATE_LAUNCH_OUTCOME_UNKNOWN';
      await ui.check();
      await ui.install(confirmed: true);
      expect(ui.canInstall, isFalse);
      await ui.install(confirmed: true);
      await ui.check();
      expect(client.calls, ['check', 'pause', 'launch:$candidate:true']);
      expect(ui.statusText, contains('无法确认'));
    },
  );
  test(
    'UAC cancellation leaves ready package and never resumes traffic',
    () async {
      client.checked = ready;
      client.launchFailure = 'UPDATE_LAUNCH_CANCELLED';
      await ui.check();
      await ui.install(confirmed: true);
      expect(ui.canInstall, isTrue);
      expect(ui.errorCode, 'UPDATE_LAUNCH_CANCELLED');
      expect(client.calls, ['check', 'pause', 'launch:$candidate:true']);
    },
  );
  test(
    'failed handoff never exits or retries an already requested installer',
    () async {
      ui.dispose();
      ui = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async {
          client.calls.add('handoff');
          return false;
        },
        exitAfterHandoff: () async {
          client.calls.add('exit');
        },
      );
      client.checked = ready;
      await ui.check();
      await ui.install(confirmed: true);
      expect(client.calls, ['check', 'launch:$candidate:true', 'handoff']);
      expect(ui.errorCode, 'UPDATE_HANDOFF_UNCONFIRMED');
      expect(ui.snapshot!.state, UpdateState.launchRequested);
      expect(ui.canInstall, isFalse);
      await ui.install(confirmed: true);
      expect(client.calls, hasLength(3));
    },
  );
}

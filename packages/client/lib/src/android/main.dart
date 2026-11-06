import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app.dart';
import 'app_controller.dart';
import 'platform/android_store.dart';
import 'platform/routing_rules_client.dart';
import 'platform/vpn_client.dart';
import 'traffic.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  LicenseRegistry.addLicense(() async* {
    yield LicenseEntryWithLineBreaks([
      'Xray-core v26.10.17',
    ], await rootBundle.loadString('native/LICENSE.Xray-core'));
    yield LicenseEntryWithLineBreaks([
      'sing-tun (TUN platform integration)',
    ], await rootBundle.loadString('native/LICENSE.sing-tun'));
    yield LicenseEntryWithLineBreaks([
      'sing (network utilities)',
    ], await rootBundle.loadString('native/LICENSE.sing'));
  });
  final store = AndroidStore();
  final source = ApiV2DataSource(
    baseUri: Uri.parse('https://client-api.sunip.vip/api/client/v1'),
    store: store,
  );
  final controller = AppController(
    source: source,
    store: store,
    vpnClient: MethodChannelVpnClient(),
    trafficClient: const MethodChannelTrafficClient(),
    routingRulesClient: const MethodChannelRoutingRulesClient(),
  );
  runApp(BlueGrassApp(controller: controller));
  controller.initialize();
}

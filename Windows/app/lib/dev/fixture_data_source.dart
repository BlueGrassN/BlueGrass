import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';

import '../app_controller.dart';
import '../preferences.dart';

/// Imported only by the explicit development entry point and tests.
class FixtureDataSource implements CustomerDataSource {
  Customer? _customer;
  @override
  Customer? get customer => _customer;
  @override
  DataSourceCapabilities get capabilities => const DataSourceCapabilities();
  @override
  Future<Customer?> restore() async => _customer;
  @override
  Future<Customer> login({
    required String username,
    required String password,
    required String deviceId,
    String deviceName = 'BlueGrass Windows',
  }) async {
    if (username.trim().isEmpty || password.isEmpty) {
      throw const ApiFailure('invalid_credentials');
    }
    return _customer = Customer(
      id: 'fixture-account',
      username: username,
      displayName: '开发预览',
    );
  }

  List<NodeRecord> get nodes => [
    _node('hk-01', '香港 · 优选 01', 'HK', '中国香港', '香港', '1.1.1.1'),
    _node('jp-01', '东京 · 低延迟', 'JP', '日本', '东京', '8.8.8.8'),
    _node('sg-01', '新加坡 · 标准', 'SG', '新加坡', '新加坡', '9.9.9.9'),
  ];
  NodeRecord _node(
    String id,
    String name,
    String code,
    String country,
    String city,
    String exit,
  ) => NodeRecord(
    key: ResourceKey(
      environment: 'fixture',
      dataSource: 'fixture-v1',
      accountKey: 'fixture-account',
      sourceId: id,
    ),
    name: name,
    protocol: 'socks5',
    host: 'relay.invalid',
    port: 1080,
    countryCode: code,
    countryName: country,
    city: city,
    expiresAtUtc: DateTime.now().toUtc().add(const Duration(days: 30)),
    expectedExitIpv4: exit,
  );
  @override
  Future<Page<NodeRecord>> listNodes({
    String? cursor,
    int perPage = 50,
  }) async => Page(items: nodes, hasMore: false);
  @override
  Future<NodeRecord> getNode(String sourceId) async =>
      nodes.firstWhere((n) => n.key.sourceId == sourceId);
  @override
  Future<void> logout() async {
    _customer = null;
  }
}

AppController fixtureController() => AppController(
  source: FixtureDataSource(),
  demo: true,
  getDeviceId: () async => '67c0b9e6-7b46-4c75-8392-c41a347e4e05',
  readPreferences: () async => const Preferences(),
  savePreferences: (_) async {},
);

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:client/platform/android_store.dart';
import 'package:flutter/material.dart' show ThemeMode;

const testCustomer = Customer(id: '1', username: 'demo', displayName: '测试用户');
final testNow = DateTime.utc(2026, 10, 13);
NodeRecord node(String id, {bool expired = false}) => NodeRecord(
  key: ResourceKey(
    environment: 'https://example.invalid',
    dataSource: 'apiV2',
    accountKey: '1',
    sourceId: id,
  ),
  name: '香港线路 $id',
  customerRemark: '办公室',
  protocol: 'socks5',
  host: 'entry.example.invalid',
  port: 1080,
  username: 'fixture-user',
  password: 'fixture-secret',
  countryName: '中国香港',
  expiresAtUtc: expired
      ? testNow.subtract(const Duration(hours: 1))
      : testNow.add(const Duration(days: 30)),
);

class MemoryStore implements AppStore {
  String routingMode = 'global';
  bool failRouting = false;
  bool failRoutingRead = false;
  int routingReads = 0;
  Future<void> Function(String)? onRoutingWrite;
  @override
  Future<String> readRoutingMode() async {
    routingReads++;
    if (failRoutingRead) throw const ApiFailure('storage_error');
    return routingMode;
  }

  @override
  Future<void> writeRoutingMode(String value) async {
    if (failRouting) throw const ApiFailure('storage_error');
    await onRoutingWrite?.call(value);
    routingMode = value;
  }

  Map<String, Object?>? snapshot;
  ThemeMode theme = ThemeMode.system;
  bool failTheme = false;
  Future<String> Function()? onDeviceId;
  @override
  Future<Map<String, Object?>?> read() async => snapshot;
  @override
  Future<void> replace(Map<String, Object?> value) async {
    snapshot = Map.of(value);
  }

  @override
  Future<void> clear() async {
    snapshot = null;
  }

  @override
  Future<String> deviceId() async =>
      onDeviceId?.call() ?? '00000000-0000-4000-8000-000000000001';
  @override
  Future<ThemeMode> readTheme() async => theme;
  @override
  Future<void> writeTheme(ThemeMode value) async {
    if (failTheme) throw const ApiFailure('storage_error');
    theme = value;
  }
}

class FakeSource implements CustomerDataSource, ResourceQueryDataSource {
  @override
  Customer? customer;
  @override
  DataSourceCapabilities capabilities = const DataSourceCapabilities(
    resourceQueries: true,
  );
  Future<Page<NodeRecord>> Function(String?)? onNodes;
  Future<NodeRecord> Function(String)? onNode;
  Future<Customer> Function()? onLogin;
  Future<void> Function()? onLogout;
  Future<DataSourceCapabilities> Function()? onCapabilities;
  Future<ResourceQueryPage> Function(String, String?)? onResources;
  final cursors = <String?>[];
  String? lastDeviceName;
  @override
  Future<Customer> login({
    required String username,
    required String password,
    required String deviceId,
    String deviceName = 'BlueGrass Windows',
  }) async {
    lastDeviceName = deviceName;
    final result = await (onLogin?.call() ?? Future.value(testCustomer));
    customer = result;
    return result;
  }

  @override
  Future<Customer?> restore() async => customer;
  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) async {
    cursors.add(cursor);
    return onNodes?.call(cursor) ??
        Page(items: [node('1'), node('2')], hasMore: false);
  }

  @override
  Future<NodeRecord> getNode(String sourceId) async =>
      onNode?.call(sourceId) ?? node(sourceId);
  @override
  Future<void> logout() async {
    customer = null;
    await onLogout?.call();
  }

  @override
  Future<DataSourceCapabilities> refreshCapabilities() async =>
      onCapabilities?.call() ?? capabilities;
  @override
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) async =>
      onResources?.call(kind, cursor) ??
      ResourceQueryPage(
        items: [
          OwnedResourceRecord(
            summary: ResourceBrief(
              key: ResourceKey(
                environment: 'https://example.invalid',
                dataSource: 'apiV2',
                accountKey: '1',
                kind: kind,
                sourceId: '1',
              ),
              name: kind == 'asset' ? '静态资产' : '全部订阅',
              connectable: false,
            ),
            status: 'expired',
          ),
        ],
        hasMore: false,
        kind: kind,
        ownershipCoverage: 'conservative',
        consistency: 'weak',
      );
  @override
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) => listResources(kind: kind, cursor: cursor, perPage: perPage);
  @override
  Future<OwnedResourceRecord> getResource(
    String sourceId, {
    String kind = 'subscription',
  }) async => throw UnimplementedError();
}

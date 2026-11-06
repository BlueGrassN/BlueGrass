import 'package:bluegrass_domain/bluegrass_domain.dart';

class Customer {
  const Customer({
    required this.id,
    required this.username,
    required this.displayName,
  });
  final String id;
  final String username;
  final String displayName;
}

class DataSourceCapabilities {
  const DataSourceCapabilities({
    this.connectableNodes = true,
    this.resourceQueries = false,
    this.allOwnedResources = false,
    this.expirySummaries = false,
    this.smsLogin = false,
    this.authoritativeExpectedExit = false,
  });
  final bool connectableNodes,
      resourceQueries,
      allOwnedResources,
      expirySummaries,
      smsLogin,
      authoritativeExpectedExit;
}

abstract interface class CustomerDataSource {
  DataSourceCapabilities get capabilities;
  Customer? get customer;
  Future<Customer> login({
    required String username,
    required String password,
    required String deviceId,
    String deviceName = 'BlueGrass Windows',
  });
  Future<Customer?> restore();
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50});
  Future<NodeRecord> getNode(String sourceId);
  Future<void> logout();
}

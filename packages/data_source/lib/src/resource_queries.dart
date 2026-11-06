import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'api_failure.dart';
import 'customer_data_source.dart';

/// Additive transport types reuse domain identities/expiry without inventing
/// a complete account snapshot or extending the domain's connection authority.
class OwnedResourceRecord {
  const OwnedResourceRecord({required this.summary, required this.status});
  final ResourceBrief summary;
  final String status;
}

class ResourceQueryPage {
  ResourceQueryPage({
    required List<OwnedResourceRecord> items,
    required bool hasMore,
    String? nextCursor,
    required this.kind,
    required this.ownershipCoverage,
    required this.consistency,
  }) : page = Page(items: items, hasMore: hasMore, nextCursor: nextCursor);
  final Page<OwnedResourceRecord> page;
  final String kind, ownershipCoverage, consistency;
  List<OwnedResourceRecord> get items => page.items;
  bool get hasMore => page.hasMore;
  String? get nextCursor => page.nextCursor;
  // A terminal cursor is traversal completion only, never deletion evidence.
  bool get snapshotComplete => false;
}

abstract interface class ResourceQueryDataSource {
  Future<DataSourceCapabilities> refreshCapabilities();
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  });
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  });
  Future<OwnedResourceRecord> getResource(
    String sourceId, {
    String kind = 'subscription',
  });
}

/// Existing CustomerDataSource implementations remain source compatible.
extension CustomerResourceQueries on CustomerDataSource {
  ResourceQueryDataSource get _queries {
    final source = this;
    if (source is ResourceQueryDataSource)
      return source as ResourceQueryDataSource;
    throw const ApiFailure('capability_unavailable');
  }

  Future<DataSourceCapabilities> refreshCapabilities() async =>
      _queries.refreshCapabilities();
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) async =>
      _queries.listResources(kind: kind, cursor: cursor, perPage: perPage);
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) async => _queries.listExpirySummaries(
    kind: kind,
    cursor: cursor,
    perPage: perPage,
  );
  Future<OwnedResourceRecord> getResource(
    String sourceId, {
    String kind = 'subscription',
  }) async => _queries.getResource(sourceId, kind: kind);
}

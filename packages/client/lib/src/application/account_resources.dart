import 'dart:async';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';

/// Account-scoped resource transactions shared by desktop and Android.
///
/// Network protection, authentication persistence and presentation remain with
/// the adapters. A new account invalidates requests immediately, but does not
/// discard its visible state until the adapter confirms it is safe to do so.
class AccountResources<T> {
  AccountResources({
    required this.source,
    required this.keyOf,
    required this.eligible,
    this.visible,
  });

  final CustomerDataSource Function() source;
  final ResourceKey Function(T) keyOf;
  final bool Function(T) eligible;
  final bool Function(T)? visible;
  Customer? customer;
  List<T> items = [];
  T? selected;
  ResourceKey? _selectedKey;
  String? cursor;
  bool loading = false, hasMore = false;
  DataSourceCapabilities capabilities = const DataSourceCapabilities();
  bool retryCapabilities = false;
  int _generation = 0, _query = 0, _capabilityQuery = 0;
  bool _disposed = false;
  final _seenCursors = <String>{};

  int get generation => _generation;
  bool current(int generation) => !_disposed && generation == _generation;

  int beginAccountChange() {
    _generation++;
    _query++;
    _capabilityQuery++;
    loading = false;
    return _generation;
  }

  void resetResources() {
    _query++;
    _capabilityQuery++;
    items = [];
    selected = null;
    _selectedKey = null;
    cursor = null;
    _seenCursors.clear();
    loading = false;
    hasMore = false;
    capabilities = const DataSourceCapabilities();
    retryCapabilities = false;
  }

  Future<DataSourceCapabilities?> refreshCapabilities() async {
    final generation = _generation, query = ++_capabilityQuery;
    try {
      final result = await source().refreshCapabilities();
      if (!current(generation) || query != _capabilityQuery) return null;
      capabilities = result;
      retryCapabilities = false;
      return result;
    } catch (_) {
      if (!current(generation)) return null;
      if (query == _capabilityQuery) retryCapabilities = true;
      rethrow;
    }
  }

  /// Commit a complete, validated traversal. Failed append requests preserve
  /// both the last good list and its cursor, so retrying cannot skip a page.
  /// Stale queries may still report session failure for the *same* account.
  Future<void> load({
    required bool reset,
    required Future<Page<T>> Function(String?) fetch,
    required void Function() changed,
    required FutureOr<void> Function(Object error, bool currentQuery) onError,
    Future<void> Function()? onLoaded,
    bool clearOnReset = false,
    bool clearSelectionOnReset = false,
    bool seekFirstEligible = false,
  }) async {
    final account = customer;
    if (_disposed || account == null) return;
    if (!reset && (loading || !hasMore)) return;
    final generation = _generation;
    final query = reset ? ++_query : _query;
    bool sameAccount() => current(generation) && customer?.id == account.id;
    bool currentQuery() => sameAccount() && query == _query;
    if (reset && clearOnReset) {
      items = [];
      cursor = null;
      hasMore = false;
      _seenCursors.clear();
    }
    if (reset && clearSelectionOnReset) selected = null;
    var next = reset ? null : cursor;
    final seen = reset ? <String>{} : {..._seenCursors};
    final merged = <ResourceKey, T>{
      if (!reset)
        for (final item in items) keyOf(item): item,
    };
    loading = true;
    changed();
    try {
      for (var count = 0; count < 100; count++) {
        final requested = next;
        final page = await fetch(requested);
        if (!currentQuery()) return;
        next = page.nextCursor;
        if ((!page.hasMore && next != null) ||
            (page.hasMore &&
                (next == null ||
                    next.isEmpty ||
                    next == requested ||
                    seen.contains(next)))) {
          throw const ApiFailure('pagination_invalid');
        }
        if (page.items.any((item) => keyOf(item).accountKey != account.id)) {
          throw const ApiFailure('invalid_response');
        }
        for (final item in page.items) {
          if (visible == null || visible!(item)) merged[keyOf(item)] = item;
        }
        if (requested != null) seen.add(requested);
        if (seekFirstEligible &&
            selected == null &&
            page.hasMore &&
            count < 99 &&
            !merged.values.any(eligible)) {
          continue;
        }
        items = List.unmodifiable(merged.values);
        cursor = page.hasMore ? next : null;
        hasMore = page.hasMore;
        _seenCursors
          ..clear()
          ..addAll(seen);
        await onLoaded?.call();
        break;
      }
    } catch (error) {
      if (sameAccount()) await onError(error, currentQuery());
    } finally {
      if (currentQuery()) {
        loading = false;
        changed();
      }
    }
  }

  /// Android waits for a remembered choice on a later page. Desktop retains a
  /// missing active choice: list absence must never revoke a live connection.
  void reconcileSelection({
    bool preserveMissing = false,
    bool eligibleOnly = true,
    bool rememberSelection = true,
  }) {
    final key = selected == null
        ? (rememberSelection ? _selectedKey : null)
        : keyOf(selected as T);
    final candidates = eligibleOnly ? items.where(eligible) : items;
    if (key == null) {
      selected = items.where(eligible).firstOrNull;
    } else {
      selected =
          candidates.where((item) => keyOf(item) == key).firstOrNull ??
          (preserveMissing ? selected : null);
    }
    if (selected != null) _selectedKey = keyOf(selected as T);
  }

  bool select(T item) {
    if (_disposed ||
        customer == null ||
        keyOf(item).accountKey != customer!.id ||
        !items.contains(item) ||
        !eligible(item)) {
      return false;
    }
    selected = item;
    _selectedKey = keyOf(item);
    return true;
  }

  void dispose() {
    beginAccountChange();
    _disposed = true;
  }
}

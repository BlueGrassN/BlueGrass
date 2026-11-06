import 'package:flutter/foundation.dart';

import 'platform/routing_rules_client.dart';

/// Optional rules metadata, independent of account and VPN initialization.
/// The visible settings card owns polling; this model owns no timers.
class RoutingRulesModel extends ChangeNotifier {
  RoutingRulesModel({this._client});

  final RoutingRulesClient? _client;
  RoutingRulesStatus? _status;
  RoutingRulesStatus? get status => _status;
  String? _error;
  String? get error => _error;
  bool _available = false;
  bool get available => _available;
  bool _refreshing = false, _updating = false, _disposed = false;
  int _revision = 0;
  Future<void>? _refresh;

  bool get updating => _updating || (_status?.updating ?? false);
  bool get loading => _refreshing && _status == null;
  bool get canUpdate => !_disposed && !_refreshing && !updating;

  void _accept(RoutingRulesStatus value) {
    _status = value;
    _available = true;
    _error = switch (value.lastError) {
      '' => null,
      'CACHE_INVALID' => '规则缓存校验失败，将继续使用可用规则。',
      'UPDATE_CANCELLED' => '规则更新已取消，可稍后重试。',
      _ => '规则更新失败，请稍后重试。',
    };
  }

  Future<void> refresh() {
    if (_disposed) return Future.value();
    return _refresh ??= _readStatus().whenComplete(() => _refresh = null);
  }

  Future<void> _readStatus() async {
    final revision = _revision;
    _refreshing = true;
    notifyListeners();
    try {
      final value = await _client?.status();
      if (value == null) throw StateError('Routing rules unavailable');
      if (!_disposed && revision == _revision) _accept(value);
    } catch (_) {
      if (!_disposed && revision == _revision) {
        _available = false;
        _error = '暂时无法读取规则状态，请稍后重试。';
      }
    } finally {
      _refreshing = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> update() async {
    if (!canUpdate) return;
    _updating = true;
    _error = null;
    _revision++;
    notifyListeners();
    try {
      final value = await _client?.update();
      if (value == null) throw StateError('Routing rules unavailable');
      if (!_disposed) _accept(value);
    } catch (_) {
      if (!_disposed) _error = '规则更新失败，请稍后重试。';
    } finally {
      // A status read started during the download must not overwrite its result.
      _revision++;
      _updating = false;
      if (!_disposed) notifyListeners();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

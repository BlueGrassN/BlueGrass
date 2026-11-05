import 'dart:convert';
import 'dart:io';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'api_failure.dart';
import 'customer_data_source.dart';
import 'http_transport.dart';
import 'session_store.dart';
import 'session_coordinator.dart';
import 'resource_queries.dart';

class ApiV2DataSource implements CustomerDataSource, ResourceQueryDataSource {
  ApiV2DataSource({
    required Uri baseUri,
    required SessionStore store,
    HttpTransport? transport,
    DateTime Function()? clock,
    String Function()? requestId,
    Future<void> Function(Duration)? delay,
    bool allowLoopbackHttp = false,
  }) : _baseUri = baseUri.replace(
         path: baseUri.path.replaceFirst(RegExp(r'/+$'), ''),
       ),
       _transport =
           transport ?? IoHttpTransport(allowLoopbackHttp: allowLoopbackHttp),
       _clock = clock ?? DateTime.now {
    validateUri(baseUri, allowLoopbackHttp: allowLoopbackHttp, base: true);
    _session = SessionCoordinator(
      store: store,
      environment: _baseUri.toString(),
      call: _call,
      clock: _clock,
      requestId: requestId,
      delay: delay,
      onAuthenticatedResponse: _acceptServerTime,
      onSessionReset: _resetServerTime,
    );
  }
  final Uri _baseUri;
  String get environment => _baseUri.toString();
  final HttpTransport _transport;
  final DateTime Function() _clock;
  late final SessionCoordinator _session;
  final _serverClock = Stopwatch()..start();
  final _responseTimes = Expando<(DateTime, Duration)>();
  DateTime? _timeAnchorUtc;
  Duration _timeAnchorElapsed = Duration.zero;

  /// Current-session HTTPS server time advanced only by a monotonic clock.
  /// Null until a successful authenticated response; never persisted or inferred
  /// from the wall clock. Callers must preserve unknown-time behavior when null.
  DateTime? get effectiveNowUtc =>
      _timeAnchorUtc?.add(_serverClock.elapsed - _timeAnchorElapsed);

  void _resetServerTime() {
    _timeAnchorUtc = null;
  }

  void _acceptServerTime(Json data) {
    final sample = _responseTimes[data];
    if (sample == null) return;
    _responseTimes[data] = null;
    final elapsed = _serverClock.elapsed;
    final candidate = sample.$1.add(elapsed - sample.$2);
    final previous = _timeAnchorUtc?.add(elapsed - _timeAnchorElapsed);
    // An older/delayed server sample cannot extend a known expiry. Acceptance
    // happens only after the coordinator validates the current generation.
    _timeAnchorUtc = previous != null && candidate.isBefore(previous)
        ? previous
        : candidate;
    _timeAnchorElapsed = elapsed;
  }

  @override
  DataSourceCapabilities get capabilities =>
      customer == null ? const DataSourceCapabilities() : _capabilities;
  DataSourceCapabilities _capabilities = const DataSourceCapabilities();
  int _capabilityRevision = 0;
  void _resetCapabilities() {
    _capabilityRevision++;
    _capabilities = const DataSourceCapabilities();
  }

  Customer _customer(Json value) => Customer(
    id: value['id'].toString(),
    username: string(value, 'username'),
    displayName: string(value, 'display_name', empty: true),
  );
  @override
  Customer? get customer {
    final value = _session.customer;
    return value == null ? null : _customer(value);
  }

  @override
  Future<Customer> login({
    required String username,
    required String password,
    required String deviceId,
    String deviceName = 'BlueGrass Windows',
  }) async {
    _resetCapabilities();
    return _customer(
      await _session.login(
        username: username,
        password: password,
        deviceId: deviceId,
        deviceName: deviceName,
      ),
    );
  }

  @override
  Future<Customer?> restore() async {
    _resetCapabilities();
    final value = await _session.restore();
    return value == null ? null : _customer(value);
  }

  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) async {
    if (perPage < 1 || perPage > 100 || cursor == '')
      throw const ApiFailure('validation_failed');
    final query = Uri(
      queryParameters: {
        'per_page': '$perPage',
        if (cursor != null) 'cursor': cursor,
      },
    ).query;
    final data = await _session.authorized('GET', '/nodes?$query');
    final items = data['items'];
    final pagination = object(data['pagination']);
    final more = pagination['has_more'];
    final next = pagination['next_cursor'];
    final count = pagination['per_page'];
    if (items is! List ||
        more is! bool ||
        count is! int ||
        count < 1 ||
        count > 100 ||
        (next != null && next is! String) ||
        (more && (next == null || next == ''))) {
      throw const ApiFailure('invalid_response');
    }
    return Page(
      items: items.map((item) => _node(object(item))).toList(),
      hasMore: more,
      nextCursor: next as String?,
    );
  }

  @override
  Future<NodeRecord> getNode(String sourceId) async {
    if (!RegExp(r'^[0-9]+$').hasMatch(sourceId) ||
        !sourceId.contains(RegExp('[1-9]')))
      throw const ApiFailure('validation_failed');
    final node = _node(
      await _session.authorized(
        'GET',
        '/nodes/${Uri.encodeComponent(sourceId)}',
      ),
    );
    if (node.key.sourceId != sourceId)
      throw const ApiFailure('invalid_response');
    return node;
  }

  @override
  Future<void> logout() {
    _resetCapabilities();
    return _session.logout();
  }

  @override
  Future<DataSourceCapabilities> refreshCapabilities() async {
    final revision = ++_capabilityRevision;
    try {
      final data = await _session.authorized('GET', '/capabilities');
      if (revision != _capabilityRevision)
        throw const ApiFailure('session_changed');
      const fields = [
        'connectable_nodes',
        'resource_queries',
        'all_owned_resources',
        'expiry_summaries',
        'sms_login',
      ];
      if (fields.any((key) => data[key] is! bool) ||
          data['ownership_coverage'] != 'conservative' ||
          data['all_owned_resources'] != false ||
          data['resource_kinds'] is! List ||
          (data['resource_kinds'] as List).length != 2 ||
          !(data['resource_kinds'] as List).contains('subscription') ||
          !(data['resource_kinds'] as List).contains('asset')) {
        throw const ApiFailure('invalid_response');
      }
      return _capabilities = DataSourceCapabilities(
        connectableNodes: data['connectable_nodes'] as bool,
        resourceQueries: data['resource_queries'] as bool,
        allOwnedResources: data['all_owned_resources'] as bool,
        expirySummaries: data['expiry_summaries'] as bool,
        smsLogin: data['sms_login'] as bool,
        // Informational metadata only; unknown/absent values cannot gate use.
        authoritativeExpectedExit: data['authoritative_expected_exit'] == true,
      );
    } on ApiFailure catch (failure) {
      if (revision != _capabilityRevision)
        throw const ApiFailure('session_changed');
      _capabilities = const DataSourceCapabilities();
      if (failure.status == 404 && failure.code == 'not_found')
        return _capabilities;
      rethrow;
    }
  }

  @override
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) =>
      _resourcePage('/resources', kind: kind, cursor: cursor, perPage: perPage);

  @override
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) => _resourcePage(
    '/resources/expiry',
    kind: kind,
    cursor: cursor,
    perPage: perPage,
  );

  bool _resourceKind(String kind) => kind == 'subscription' || kind == 'asset';

  Future<ResourceQueryPage> _resourcePage(
    String path, {
    required String kind,
    String? cursor,
    required int perPage,
  }) async {
    if (!_resourceKind(kind) || perPage < 1 || perPage > 100 || cursor == '')
      throw const ApiFailure('validation_failed');
    final query = Uri(
      queryParameters: {
        'kind': kind,
        'per_page': '$perPage',
        if (cursor != null) 'cursor': cursor,
      },
    ).query;
    final data = await _session.authorized('GET', '$path?$query');
    final items = data['items'];
    final pagination = object(data['pagination']);
    final coverage = object(data['coverage']);
    final next = pagination['next_cursor'];
    final more = pagination['has_more'];
    if (items is! List ||
        items.length > perPage ||
        more is! bool ||
        pagination['per_page'] != perPage ||
        (next != null && (next is! String || next.isEmpty)) ||
        (more && (next == null || next == cursor)) ||
        (!more && next != null) ||
        coverage['kind'] != kind ||
        coverage['consistency'] != 'per_page' ||
        coverage['ownership'] != 'conservative' ||
        coverage['snapshot_complete'] != false) {
      throw const ApiFailure('invalid_response');
    }
    final records = items
        .map((value) => _resource(object(value), kind))
        .toList();
    if (records.map((value) => value.summary.key).toSet().length !=
        records.length)
      throw const ApiFailure('invalid_response');
    return ResourceQueryPage(
      items: records,
      hasMore: more,
      nextCursor: next as String?,
      kind: kind,
      consistency: 'per_page',
      ownershipCoverage: 'conservative',
    );
  }

  @override
  Future<OwnedResourceRecord> getResource(
    String sourceId, {
    String kind = 'subscription',
  }) async {
    if (!_resourceKind(kind) || !_resourceId(sourceId))
      throw const ApiFailure('validation_failed');
    final value = _resource(
      await _session.authorized('GET', '/resources/$kind/$sourceId'),
      kind,
    );
    if (value.summary.key.sourceId != sourceId)
      throw const ApiFailure('invalid_response');
    return value;
  }

  bool _resourceId(String value) =>
      RegExp(r'^[1-9][0-9]{0,18}$').hasMatch(value) &&
      (int.tryParse(value) ?? 0) > 0;

  String _customerRemark(Json data) => data.containsKey('customer_remark')
      ? string(data, 'customer_remark', empty: true).trim()
      : '';

  OwnedResourceRecord _resource(Json data, String kind) {
    final id = string(data, 'id');
    if (!_resourceId(id) ||
        data['kind'] != kind ||
        data['owned'] != true ||
        data['connectable'] != false ||
        !data.containsKey('expires_at'))
      throw const ApiFailure('invalid_response');
    final account = customer;
    if (account == null) throw const ApiFailure('session_changed');
    return OwnedResourceRecord(
      summary: ResourceBrief(
        key: ResourceKey(
          environment: _baseUri.toString(),
          dataSource: 'api_v2',
          accountKey: account.id,
          kind: kind,
          sourceId: id,
        ),
        name: string(data, 'name'),
        customerRemark: _customerRemark(data),
        expiresAtUtc: data['expires_at'] == null
            ? null
            : timestamp(data['expires_at']),
        owned: true,
        connectable: false,
      ),
      status: string(data, 'status'),
    );
  }

  NodeRecord _node(Json data) {
    const required = {
      'id',
      'name',
      'protocol',
      'host',
      'port',
      'country_code',
      'country_name',
      'city',
      'expires_at',
      'is_forwarded',
    };
    final port = data['port'];
    if (!required.every(data.containsKey) ||
        data['is_forwarded'] != true ||
        port is! int ||
        port < 1 ||
        port > 65535) {
      throw const ApiFailure('invalid_response');
    }
    final protocol = string(data, 'protocol');
    final explicit = data.containsKey('protocol_config');
    final username = explicit && !data.containsKey('username')
        ? ''
        : string(data, 'username', empty: true);
    final password = explicit && !data.containsKey('password')
        ? ''
        : string(data, 'password', empty: true);
    ProtocolConfig? protocolConfig;
    try {
      if (explicit) {
        protocolConfig = ProtocolConfig.fromJson(data['protocol_config']);
        if (!protocolConfig.matchesNode(
          protocol: protocol,
          username: username,
          password: password,
        )) {
          throw const FormatException('INVALID_PROTOCOL_CONFIG');
        }
      } else {
        if (protocol != 'socks5') {
          throw const FormatException('INVALID_PROTOCOL_CONFIG');
        }
        ProtocolConfig.legacySocks(username: username, password: password);
      }
    } on FormatException {
      throw const ApiFailure('invalid_response');
    }
    final exit = data['expected_exit_ip'];
    final expected = exit is String && isPublicIpv4(exit) ? exit : null;
    final account = customer;
    if (account == null) throw const ApiFailure('session_changed');
    return NodeRecord(
      key: ResourceKey(
        environment: _baseUri.toString(),
        dataSource: 'api_v2',
        accountKey: account.id,
        sourceId: string(data, 'id'),
      ),
      name: string(data, 'name'),
      customerRemark: _customerRemark(data),
      protocol: protocol,
      protocolConfig: protocolConfig,
      host: string(data, 'host'),
      port: port,
      username: username,
      password: password,
      countryCode: string(data, 'country_code', empty: true),
      countryName: string(data, 'country_name', empty: true),
      city: string(data, 'city', empty: true),
      expiresAtUtc: data['expires_at'] == null
          ? null
          : timestamp(data['expires_at']),
      expectedExitIpv4: expected,
      isForwarded: true,
    );
  }

  Duration? _retryAfter(Map<String, String> headers) {
    String? value;
    for (final entry in headers.entries) {
      if (entry.key.toLowerCase() == 'retry-after') value = entry.value;
    }
    if (value == null) return null;
    final seconds = int.tryParse(value.trim());
    if (seconds != null && seconds >= 0 && seconds <= 2147483647)
      return Duration(seconds: seconds);
    try {
      final remaining = HttpDate.parse(value).difference(_clock().toUtc());
      return remaining < Duration.zero ? Duration.zero : remaining;
    } catch (_) {
      return null;
    }
  }

  Future<Json> _call(
    String method,
    String path, {
    Json? body,
    String? accessToken,
  }) async {
    ApiResponse response;
    try {
      response = await _transport.send(
        ApiRequest(
          method: method,
          uri: Uri.parse('$_baseUri$path'),
          headers: {
            'Accept': 'application/json',
            if (body != null) 'Content-Type': 'application/json',
            if (accessToken != null) 'Authorization': 'Bearer $accessToken',
          },
          body: body,
        ),
      );
    } on ApiFailure {
      rethrow;
    } catch (_) {
      throw const ApiFailure('network_error');
    }
    final receivedAt = _serverClock.elapsed;
    final retryAfter = _retryAfter(response.headers);
    try {
      final envelope = object(decodeStrictJson(utf8.decode(response.body)));
      final serverTime = timestamp(envelope['server_time']);
      if (response.status >= 200 &&
          response.status < 300 &&
          envelope['success'] == true) {
        final data = object(envelope['data']);
        // Keep timing out of DTOs and durable token storage. Login/refresh must
        // finish validation and persistence before this sample can be trusted.
        if (_baseUri.scheme == 'https') {
          _responseTimes[data] = (serverTime, receivedAt);
        }
        return data;
      }
      if (response.status >= 400 && envelope['success'] == false) {
        final error = object(envelope['error']);
        final code = string(error, 'code');
        string(error, 'message', empty: true);
        throw ApiFailure(code, status: response.status, retryAfter: retryAfter);
      }
    } on ApiFailure catch (failure) {
      if (failure.status != null) rethrow;
    } catch (_) {
      /* Malformed input is replaced with a safe contract error. */
    }
    throw ApiFailure(
      'invalid_response',
      status: response.status,
      retryAfter: retryAfter,
    );
  }
}

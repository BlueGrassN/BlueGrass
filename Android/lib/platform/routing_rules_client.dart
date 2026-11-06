import 'dart:convert';

import 'package:flutter/services.dart';

class RoutingRulesStatus {
  const RoutingRulesStatus._({
    required this.version,
    required this.source,
    required this.checkedAt,
    required this.updatedAt,
    required this.updating,
    required this.lastError,
  });

  final String version, source, lastError;
  final DateTime? checkedAt, updatedAt;
  final bool updating;

  factory RoutingRulesStatus.fromJson(String json) {
    final value = jsonDecode(json);
    // An unavailable native bridge may return an empty version. Reject that
    // snapshot so the model retains its last valid version and timestamps.
    if (value is! Map<String, dynamic> ||
        value['version'] is! String ||
        !RegExp(r'^\d{12}$').hasMatch(value['version'] as String) ||
        !const {'bundled', 'downloaded'}.contains(value['source']) ||
        value['updating'] is! bool ||
        value['lastError'] is! String) {
      throw const FormatException('Invalid routing rules status');
    }
    final error = value['lastError'] as String;
    return RoutingRulesStatus._(
      version: value['version'] as String,
      source: value['source'] as String,
      checkedAt: _timestamp(value['checkedAt']),
      updatedAt: _timestamp(value['updatedAt']),
      updating: value['updating'] as bool,
      // Native messages and future unknown codes never become UI text.
      lastError:
          const {
            '',
            'UPDATE_FAILED',
            'CACHE_INVALID',
            'UPDATE_CANCELLED',
          }.contains(error)
          ? error
          : 'UPDATE_FAILED',
    );
  }

  static DateTime? _timestamp(Object? value) {
    if (value == '') return null;
    if (value is! String ||
        !RegExp(r'^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|\+00:00)$')
            .hasMatch(value)) {
      throw const FormatException('Invalid routing rules timestamp');
    }
    final parsed = DateTime.tryParse(value);
    if (parsed == null ||
        parsed.toIso8601String().substring(0, 19) != value.substring(0, 19)) {
      throw const FormatException('Invalid routing rules timestamp');
    }
    return parsed;
  }
}

abstract interface class RoutingRulesClient {
  Future<RoutingRulesStatus> status();
  Future<RoutingRulesStatus> update();
}

class MethodChannelRoutingRulesClient implements RoutingRulesClient {
  const MethodChannelRoutingRulesClient();
  static const _channel = MethodChannel('bluegrass/routing-rules');

  Future<RoutingRulesStatus> _invoke(String method) async {
    final json = await _channel.invokeMethod<String>(method);
    if (json == null) {
      throw const FormatException('Missing routing rules status');
    }
    return RoutingRulesStatus.fromJson(json);
  }

  @override
  Future<RoutingRulesStatus> status() => _invoke('status');

  // No timeout: the native background download can legitimately take minutes.
  @override
  Future<RoutingRulesStatus> update() => _invoke('update');
}

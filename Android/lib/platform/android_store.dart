import 'dart:convert';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/material.dart' show ThemeMode;
import 'package:flutter/services.dart';

import '../routing_mode.dart';

abstract interface class AppStore implements SessionStore {
  Future<String> deviceId();
  Future<ThemeMode> readTheme();
  Future<void> writeTheme(ThemeMode value);
  Future<String> readRoutingMode();
  Future<void> writeRoutingMode(String value);
}

class AndroidStore implements AppStore {
  static const _channel = MethodChannel('bluegrass/android');
  Future<T?> _invoke<T>(String method, [Map<String, Object?>? args]) async {
    try {
      return await _channel.invokeMethod<T>(method, args);
    } catch (_) {
      // Native errors must never expose a path, token or nested exception.
      throw const ApiFailure('storage_error');
    }
  }

  @override
  Future<Map<String, Object?>?> read() async {
    final encoded = await _invoke<String>('sessionRead');
    if (encoded == null) return null;
    try {
      final value = jsonDecode(encoded);
      if (value is! Map<String, dynamic>) throw const FormatException();
      return Map<String, Object?>.from(value);
    } catch (_) {
      throw const ApiFailure('storage_error');
    }
  }

  @override
  Future<void> replace(Map<String, Object?> snapshot) async {
    try {
      await _invoke<void>('sessionReplace', {'value': jsonEncode(snapshot)});
    } catch (_) {
      throw const ApiFailure('storage_error');
    }
  }

  @override
  Future<void> clear() => _invoke<void>('sessionClear');
  @override
  Future<String> deviceId() async {
    final value = await _invoke<String>('deviceId');
    if (value == null ||
        !RegExp(
          r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
        ).hasMatch(value)) {
      throw const ApiFailure('storage_error');
    }
    return value;
  }

  @override
  Future<ThemeMode> readTheme() async =>
      switch (await _invoke<String>('themeRead')) {
        'system' => ThemeMode.system,
        'light' => ThemeMode.light,
        'dark' => ThemeMode.dark,
        _ => throw const ApiFailure('storage_error'),
      };
  @override
  Future<void> writeTheme(ThemeMode value) =>
      _invoke<void>('themeWrite', {'value': value.name});

  @override
  Future<String> readRoutingMode() async {
    final mode = await _invoke<String>('routingModeRead');
    if (!routingModes.contains(mode)) throw const ApiFailure('storage_error');
    return mode!;
  }

  @override
  Future<void> writeRoutingMode(String value) async {
    if (!routingModes.contains(value)) throw const ApiFailure('storage_error');
    await _invoke<void>('routingModeWrite', {'value': value});
  }
}

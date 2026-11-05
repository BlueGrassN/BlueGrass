import 'dart:convert';

import 'package:flutter/material.dart';

import 'platform_store.dart';
import 'entry_dns_policy.dart';

class Preferences {
  const Preferences({
    this.theme = ThemeMode.system,
    this.mode = 'system_proxy',
    this.routingMode = 'bypass_cn',
    this.allowLan = true,
    this.startWithWindows = false,
    this.autoConnect = false,
    this.startupPromptShown = false,
    this.socksWarningShown = false,
    this.entryDns = const EntryDnsPolicy.auto(),
  }) : assert(routingMode == 'global' || routingMode == 'bypass_cn');
  final ThemeMode theme;
  final String mode;
  final String routingMode;
  final bool allowLan;
  final EntryDnsPolicy entryDns;
  final bool startWithWindows,
      autoConnect,
      startupPromptShown,
      socksWarningShown;

  Preferences copyWith({
    ThemeMode? theme,
    String? mode,
    String? routingMode,
    bool? allowLan,
    bool? startWithWindows,
    bool? autoConnect,
    bool? startupPromptShown,
    bool? socksWarningShown,
    EntryDnsPolicy? entryDns,
  }) => Preferences(
    theme: theme ?? this.theme,
    mode: mode ?? this.mode,
    routingMode: routingMode ?? this.routingMode,
    allowLan: allowLan ?? this.allowLan,
    startWithWindows: startWithWindows ?? this.startWithWindows,
    autoConnect: autoConnect ?? this.autoConnect,
    startupPromptShown: startupPromptShown ?? this.startupPromptShown,
    socksWarningShown: socksWarningShown ?? this.socksWarningShown,
    entryDns: entryDns ?? this.entryDns,
  );

  Map<String, Object?> toJson() => {
    'theme': theme.name,
    'mode': mode,
    'routingMode': routingMode,
    'allowLan': allowLan,
    'entryDns': entryDns.toJson(),
    if (startWithWindows) 'startWithWindows': true,
    if (autoConnect) 'autoConnect': true,
    if (startupPromptShown) 'startupPromptShown': true,
    if (socksWarningShown) 'socksWarningShown': true,
  };

  factory Preferences.fromJson(Map<String, dynamic> value) => Preferences(
    theme:
        ThemeMode.values.where((v) => v.name == value['theme']).firstOrNull ??
        ThemeMode.system,
    mode: value['mode'] == 'tun' ? 'tun' : 'system_proxy',
    // Existing installs keep their all-proxy behavior. Malformed stored values
    // take the same conservative fallback instead of enabling direct routing.
    routingMode: value['routingMode'] == 'bypass_cn' ? 'bypass_cn' : 'global',
    allowLan: value['allowLan'] is bool ? value['allowLan'] as bool : true,
    startWithWindows: value['startWithWindows'] == true,
    autoConnect: value['autoConnect'] == true,
    startupPromptShown: value['startupPromptShown'] == true,
    socksWarningShown: value['socksWarningShown'] == true,
    entryDns: value.containsKey('entryDns')
        ? EntryDnsPolicy.fromJson(value['entryDns'])
        : const EntryDnsPolicy.auto(),
  );

  static Future<Preferences> read() async {
    final data = await platform.invokeMethod<String>('readRecord', {
      'key': 'preferences',
    });
    if (data == null) return const Preferences();
    final decoded = jsonDecode(data);
    if (decoded is! Map<String, dynamic>) {
      throw const FormatException('Invalid preferences');
    }
    return Preferences.fromJson(decoded);
  }

  Future<void> save() => platform.invokeMethod<void>('writeRecord', {
    'key': 'preferences',
    'value': jsonEncode(toJson()),
  });
}

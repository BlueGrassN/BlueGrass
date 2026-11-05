import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';

const defaultApiUrl = 'https://client-api.sunip.vip/api/client/v1';
const clientConfigUrl =
    'https://client-api.sunip.vip/client-config/config.json';

/// Shared by every client platform. Only the fixed HTTPS bootstrap may select
/// another API origin; API responses and redirects cannot change the endpoint.
class ApiEndpointConfig {
  const ApiEndpointConfig({required this.revision, required this.baseUri});
  final int revision;
  final Uri baseUri;

  factory ApiEndpointConfig.parse(Map<String, dynamic> json) {
    if (json.length != 3 ||
        json['version'] != 1 ||
        json['revision'] is! int ||
        (json['revision'] as int) < 1 ||
        (json['revision'] as int) > 9007199254740991 ||
        json['api_base_url'] is! String) {
      throw const FormatException('Invalid API configuration');
    }
    final uri = Uri.parse(json['api_base_url'] as String);
    if (!validApiBase(uri) || uri.toString() != json['api_base_url']) {
      throw const FormatException('Invalid API endpoint');
    }
    return ApiEndpointConfig(revision: json['revision'] as int, baseUri: uri);
  }

  Map<String, Object?> toJson() => {
    'version': 1,
    'revision': revision,
    'api_base_url': baseUri.toString(),
  };
}

bool validApiBase(Uri uri) =>
    uri.scheme == 'https' &&
    uri.port == 443 &&
    !uri.hasPort &&
    uri.userInfo.isEmpty &&
    !uri.hasQuery &&
    !uri.hasFragment &&
    uri.path == '/api/client/v1' &&
    uri.toString() == 'https://${uri.host}/api/client/v1' &&
    validApiHost(uri.host);

bool validApiHost(String host) {
  if (host.length > 253 || InternetAddress.tryParse(host) != null) return false;
  final labels = host.split('.');
  return labels.length >= 2 &&
      RegExp(r'^[a-z]{2,63}$').hasMatch(labels.last) &&
      !const {
        'local',
        'localhost',
        'internal',
        'invalid',
        'test',
      }.contains(labels.last) &&
      labels.every(
        (label) =>
            RegExp(r'^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$').hasMatch(label),
      );
}

Future<Uri> loadApiEndpoint({
  required SessionStore store,
  HttpTransport? transport,
  bool allowNetwork = true,
}) async {
  ApiEndpointConfig? cached;
  try {
    final value = await store.read();
    if (value != null) cached = ApiEndpointConfig.parse(value);
  } catch (_) {
    // A damaged cache must not prevent using the compiled production API.
  }
  if (allowNetwork) {
    try {
      final response =
          await (transport ??
                  IoHttpTransport(
                    timeout: const Duration(seconds: 5),
                    maxResponseBytes: 4096,
                  ))
              .send(
                ApiRequest(
                  method: 'GET',
                  uri: Uri.parse(clientConfigUrl),
                  headers: const {'Accept': 'application/json'},
                ),
              );
      if (response.status == 200 && response.body.length <= 4096) {
        final value = jsonDecode(utf8.decode(response.body));
        if (value is Map<String, dynamic>) {
          final candidate = ApiEndpointConfig.parse(value);
          if (cached == null ||
              candidate.revision > cached.revision ||
              (candidate.revision == cached.revision &&
                  candidate.baseUri == cached.baseUri)) {
            cached = candidate;
            try {
              await store.replace(candidate.toJson());
            } catch (_) {
              // The authenticated configuration still applies to this launch.
            }
          }
        }
      }
    } catch (_) {
      // Missing config, TLS/network failures and invalid JSON retain the cache.
    }
  }
  return cached?.baseUri ?? Uri.parse(defaultApiUrl);
}

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'api_failure.dart';

void validateUri(
  Uri uri, {
  required bool allowLoopbackHttp,
  bool base = false,
}) {
  final loopback = InternetAddress.tryParse(uri.host)?.isLoopback == true;
  if (uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasFragment ||
      (base && uri.hasQuery) ||
      (uri.scheme != 'https' &&
          !(allowLoopbackHttp && uri.scheme == 'http' && loopback))) {
    throw const ApiFailure('insecure_endpoint');
  }
}

class ApiRequest {
  const ApiRequest({
    required this.method,
    required this.uri,
    this.headers = const {},
    this.body,
  });
  final String method;
  final Uri uri;
  final Map<String, String> headers;
  final Map<String, Object?>? body;
}

class ApiResponse {
  const ApiResponse({
    required this.status,
    required this.body,
    this.headers = const {},
  });
  final int status;
  final List<int> body;
  final Map<String, String> headers;
}

abstract interface class HttpTransport {
  Future<ApiResponse> send(ApiRequest request);
}

class IoHttpTransport implements HttpTransport {
  IoHttpTransport({
    this.allowLoopbackHttp = false,
    this.timeout = const Duration(seconds: 20),
    this.maxResponseBytes = 2 * 1024 * 1024,
    this.explicitProxy,
  });

  final Uri? explicitProxy;
  final bool allowLoopbackHttp;
  final Duration timeout;
  final int maxResponseBytes;
  @override
  Future<ApiResponse> send(ApiRequest request) async {
    validateUri(request.uri, allowLoopbackHttp: allowLoopbackHttp);
    final proxy = explicitProxy;
    if (proxy != null &&
        (proxy.scheme != 'http' ||
            InternetAddress.tryParse(proxy.host)?.isLoopback != true ||
            proxy.userInfo.isNotEmpty ||
            proxy.hasQuery ||
            proxy.hasFragment ||
            (proxy.path.isNotEmpty && proxy.path != '/') ||
            !proxy.hasPort ||
            proxy.port < 1)) {
      throw const ApiFailure('invalid_explicit_proxy');
    }
    if (timeout <= Duration.zero || maxResponseBytes <= 0)
      throw const ApiFailure('invalid_transport_limits');
    final client = HttpClient()
      ..connectionTimeout = timeout
      ..findProxy = (_) =>
          proxy == null ? 'DIRECT' : 'PROXY ${proxy.authority}';
    // Use platform certificate verification; never set badCertificateCallback.
    Future<ApiResponse> perform() async {
      final bytes = request.body == null
          ? null
          : utf8.encode(jsonEncode(request.body));
      if (bytes != null && bytes.length > 4096)
        throw const ApiFailure('payload_too_large');
      final outgoing = await client.openUrl(request.method, request.uri);
      outgoing.followRedirects = false;
      request.headers.forEach(outgoing.headers.set);
      if (bytes != null) outgoing.add(bytes);
      final incoming = await outgoing.close();
      if (incoming.contentLength > maxResponseBytes)
        throw const ApiFailure('response_too_large');
      final body = <int>[];
      await for (final chunk in incoming) {
        if (body.length + chunk.length > maxResponseBytes)
          throw const ApiFailure('response_too_large');
        body.addAll(chunk);
      }
      final headers = <String, String>{};
      incoming.headers.forEach((name, values) {
        headers[name.toLowerCase()] = values.join(', ');
      });
      return ApiResponse(
        status: incoming.statusCode,
        body: body,
        headers: headers,
      );
    }

    try {
      return await perform().timeout(timeout);
    } on TimeoutException {
      throw const ApiFailure('timeout');
    } on ApiFailure {
      rethrow;
    } catch (_) {
      throw const ApiFailure('network_error');
    } finally {
      client.close(force: true);
    }
  }
}

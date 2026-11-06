/// Does not retain server messages, URIs, response bodies or nested exceptions.
class ApiFailure implements Exception {
  const ApiFailure(this.code, {this.status, this.retryAfter});
  final String code;
  final int? status;
  final Duration? retryAfter;
  bool get transient =>
      code == 'network_error' ||
      code == 'timeout' ||
      status == 429 ||
      status == 503;
  bool get terminal =>
      (status == 401 &&
          const {
            'credentials_changed',
            'account_unavailable',
            'session_revoked',
            'session_expired',
            'invalid_refresh_token',
            'refresh_reuse_detected',
          }.contains(code)) ||
      (status == 403 && code == 'account_disabled');
  @override
  String toString() => 'ApiFailure(status: ${status ?? "local"})';
}

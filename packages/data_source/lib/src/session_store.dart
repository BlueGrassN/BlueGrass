/// One encrypted record, owned by one SessionCoordinator in one UI process.
/// Implementations must return detached JSON values and atomically/durably replace
/// the complete record. Never fall back to plaintext if secure storage fails.
abstract interface class SessionStore {
  Future<Map<String, Object?>?> read();
  Future<void> replace(Map<String, Object?> snapshot);
  Future<void> clear();
}

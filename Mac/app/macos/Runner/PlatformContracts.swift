import Foundation
import CoreFoundation

// Only fixed, non-sensitive codes cross the channel. Never attach arguments,
// Foundation error descriptions, URLs, record keys, or Keychain payloads.
enum PlatformFailure: String, Error {
  case invalidArguments = "invalid_arguments"
  case invalidRecordKey = "invalid_record_key"
  case recordTooLarge = "record_too_large"
  case invalidRecord = "invalid_record"
  case keychainUnavailable = "keychain_unavailable"
  case invalidURL = "invalid_url"
  case openURLFailed = "open_url_failed"
  case windowUnavailable = "window_unavailable"
  case busy
  case exportFailed = "export_failed"
  case notificationUnavailable = "notification_unavailable"
  case startupFailed = "startup_failed"
  case startupApprovalRequired = "startup_approval_required"
  case unsupportedCapability = "unsupported_capability"
}

struct PlatformArguments {
  let values: [String: Any]

  init(_ arguments: Any?) throws {
    guard let values = arguments as? [String: Any], values.count <= 12 else {
      throw PlatformFailure.invalidArguments
    }
    self.values = values
  }

  func string(_ key: String, maxBytes: Int, allowEmpty: Bool = false) throws -> String {
    guard let value = values[key] as? String,
          (allowEmpty || !value.isEmpty), value.utf8.count <= maxBytes else {
      throw PlatformFailure.invalidArguments
    }
    return value
  }

  func bool(_ key: String) throws -> Bool {
    guard let value = values[key] as? NSNumber,
          CFGetTypeID(value) == CFBooleanGetTypeID() else {
      throw PlatformFailure.invalidArguments
    }
    return value.boolValue
  }

  func argb(_ key: String) throws -> UInt32 {
    guard let value = values[key] as? NSNumber,
          CFGetTypeID(value) != CFBooleanGetTypeID(),
          !["f", "d"].contains(String(cString: value.objCType)),
          value.int64Value >= 0, value.uint64Value <= UInt64(UInt32.max) else {
      throw PlatformFailure.invalidArguments
    }
    return value.uint32Value
  }
}

enum PlatformValidation {
  static let maxRecordBytes = 1_048_576
  static let maxExportBytes = 8_388_608

  static func recordKey(_ key: String) throws -> String {
    let allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
    guard !key.isEmpty, key.utf8.count <= 100,
          key.utf8.allSatisfy({ allowed.utf8.contains($0) }) else {
      throw PlatformFailure.invalidRecordKey
    }
    return key
  }

  static func recordValue(_ value: String) throws -> Data {
    guard value.utf8.count <= maxRecordBytes else { throw PlatformFailure.recordTooLarge }
    return Data(value.utf8)
  }

  static func externalURL(_ raw: String) throws -> URL {
    guard !raw.isEmpty, raw.utf8.count <= 4096,
          !raw.unicodeScalars.contains(where: { CharacterSet.whitespacesAndNewlines.contains($0)
            || CharacterSet.controlCharacters.contains($0) }),
          let decoded = raw.removingPercentEncoding,
          !decoded.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }),
          let parts = URLComponents(string: raw), parts.scheme?.lowercased() == "https",
          let host = parts.host, !host.isEmpty, parts.user == nil, parts.password == nil,
          parts.port == nil || (1...65535).contains(parts.port!),
          let url = parts.url else { throw PlatformFailure.invalidURL }
    return url
  }

  static func exportName(_ name: String) throws -> String {
    guard !name.isEmpty, name != ".", name != "..", name.utf8.count <= 200,
          !name.contains("/"), !name.contains("\\"), !name.contains(":"),
          !name.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) else {
      throw PlatformFailure.invalidArguments
    }
    return name
  }

  static func exportContent(_ text: String) throws -> Data {
    guard text.utf8.count <= maxExportBytes else { throw PlatformFailure.invalidArguments }
    return Data(text.utf8)
  }

  static func notificationID(_ id: String) throws -> String {
    guard !id.isEmpty, id.utf8.count <= 256,
          !id.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) else {
      throw PlatformFailure.invalidArguments
    }
    return id
  }
}

// Main-thread state. Returning from exitRequested is NOT an acknowledgement:
// Dart may return after a failed disconnect. Only Dart's explicit exit is one.
final class ExitHandshake {
  private(set) var mayTerminate = false
  private var requestInFlight = false

  func beginRequest() -> Bool {
    guard !mayTerminate, !requestInFlight else { return false }
    requestInFlight = true
    return true
  }

  func requestReturned() { requestInFlight = false }
  func acknowledgeCleanup() { mayTerminate = true }
}

struct NotificationActivationQueue {
  private var values: [String] = []
  mutating func append(_ value: String) {
    guard !values.contains(value) else { return }
    if values.count == 32 { values.removeFirst() }
    values.append(value)
  }
  mutating func take() -> String? { values.isEmpty ? nil : values.removeFirst() }
  mutating func remove(_ value: String) { values.removeAll { $0 == value } }
}

import Foundation
import Security

protocol KeychainBackend {
  func read(_ query: [String: Any]) -> (OSStatus, Data?)
  func update(_ query: [String: Any], attributes: [String: Any]) -> OSStatus
  func add(_ attributes: [String: Any]) -> OSStatus
  func delete(_ query: [String: Any]) -> OSStatus
}

struct SystemKeychainBackend: KeychainBackend {
  func read(_ query: [String: Any]) -> (OSStatus, Data?) {
    var item: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &item)
    return (status, item as? Data)
  }
  func update(_ query: [String: Any], attributes: [String: Any]) -> OSStatus {
    SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
  }
  func add(_ attributes: [String: Any]) -> OSStatus { SecItemAdd(attributes as CFDictionary, nil) }
  func delete(_ query: [String: Any]) -> OSStatus { SecItemDelete(query as CFDictionary) }
}

final class KeychainRecordStore {
  private let backend: KeychainBackend
  private let service: String

  init(backend: KeychainBackend = SystemKeychainBackend(),
       service: String = "com.sunipip.bluegrass.macos.records") {
    self.backend = backend
    self.service = service
  }

  private func query(_ key: String) throws -> [String: Any] {
    [kSecClass as String: kSecClassGenericPassword,
     kSecAttrService as String: service,
     kSecAttrAccount as String: try PlatformValidation.recordKey(key),
     kSecAttrSynchronizable as String: false,
     kSecUseAuthenticationUI as String: kSecUseAuthenticationUIFail]
  }

  // The host runs all operations on one serial worker; no Keychain UI or logs.
  func read(_ key: String) throws -> String? {
    var request = try query(key)
    request[kSecReturnData as String] = true
    request[kSecMatchLimit as String] = kSecMatchLimitOne
    let (status, data) = backend.read(request)
    if status == errSecItemNotFound { return nil }
    guard status == errSecSuccess else { throw PlatformFailure.keychainUnavailable }
    guard let data = data, data.count <= PlatformValidation.maxRecordBytes,
          let value = String(data: data, encoding: .utf8) else { throw PlatformFailure.invalidRecord }
    return value
  }

  func write(_ key: String, value: String) throws {
    let request = try query(key)
    let data = try PlatformValidation.recordValue(value)
    let attributes = [kSecValueData as String: data]
    var status = backend.update(request, attributes: attributes)
    if status == errSecItemNotFound {
      var insertion = request
      insertion[kSecValueData as String] = data
      insertion[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
      status = backend.add(insertion)
      // Another process can win the insert. Replace atomically, never delete
      // first: failed updates must leave the previous complete record intact.
      if status == errSecDuplicateItem { status = backend.update(request, attributes: attributes) }
    }
    guard status == errSecSuccess else { throw PlatformFailure.keychainUnavailable }
  }

  func delete(_ key: String) throws {
    let status = backend.delete(try query(key))
    guard status == errSecSuccess || status == errSecItemNotFound else {
      throw PlatformFailure.keychainUnavailable
    }
  }
}

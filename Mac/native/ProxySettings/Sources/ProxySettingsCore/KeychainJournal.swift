import Foundation
import Security
import Darwin

final class KeychainJournal {
    private let lifetime: OperationLifetime
    init(lifetime: OperationLifetime) { self.lifetime = lifetime }
    private var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: "org.bluegrass.proxy-settings.recovery.v1",
         kSecAttrAccount as String: "uid:\(getuid())",
         kSecAttrSynchronizable as String: false]
    }
    func load() throws -> Data? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        var status: OSStatus = errSecNotAvailable
        try lifetime.afterWait({ status = SecItemCopyMatching(q as CFDictionary, &result) }, then: {})
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw ProxyFailure.recoveryRequired }
        try ProxyRequest.validateRecord(data)
        return data
    }
    func save(_ data: Data) throws {
        try ProxyRequest.validateRecord(data)
        let attrs = [kSecValueData as String: data]
        var status: OSStatus = errSecNotAvailable
        try lifetime.afterWait({ status = SecItemUpdate(query as CFDictionary, attrs as CFDictionary) }, then: {})
        if status == errSecItemNotFound {
            var item = query
            item[kSecValueData as String] = data
            item[kSecAttrLabel as String] = "BlueGrass proxy recovery"
            // Use the ordinary login Keychain. The data-protection Keychain
            // would require signing entitlements unavailable in this build.
            try lifetime.afterWait({ status = SecItemAdd(item as CFDictionary, nil) }, then: {})
            guard status == errSecSuccess else { throw ProxyFailure.recoveryRequired }
        } else if status != errSecSuccess { throw ProxyFailure.recoveryRequired }
    }
    func delete() throws {
        var status: OSStatus = errSecNotAvailable
        try lifetime.afterWait({ status = SecItemDelete(query as CFDictionary) }, then: {})
        guard status == errSecSuccess || status == errSecItemNotFound else { throw ProxyFailure.recoveryRequired }
    }
}

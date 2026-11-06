import Foundation
import SystemConfiguration
import Security

final class SystemPreferences {
    private let lifetime: OperationLifetime
    private var authorization: AuthorizationRef?
    private var authorized = false

    init(lifetime: OperationLifetime) throws {
        self.lifetime = lifetime
        try lifetime.check()
        guard AuthorizationCreate(nil, nil, [], &authorization) == errAuthorizationSuccess else {
            throw ProxyFailure.recoveryRequired
        }
    }
    deinit { if let ref = authorization { AuthorizationFree(ref, []) } }

    private func authorize() throws {
        try lifetime.check()
        if authorized { return }
        guard let reference = authorization else { throw ProxyFailure.recoveryRequired }
        // The right authorizes only system network preferences. We never launch
        // a privileged command or ask for general root execution.
        var status: OSStatus = errAuthorizationDenied
        try lifetime.afterWait({
            status = "system.preferences.network".withCString { name in
                var item = AuthorizationItem(name: name, valueLength: 0, value: nil, flags: 0)
                return withUnsafeMutablePointer(to: &item) { pointer -> OSStatus in
                    var rights = AuthorizationRights(count: 1, items: pointer)
                    return AuthorizationCopyRights(reference, &rights, nil,
                                                   [.interactionAllowed, .extendRights], nil)
                }
            }
        }, then: {})
        guard status == errAuthorizationSuccess else { throw ProxyFailure.recoveryRequired }
        authorized = true
    }
    private func session(write: Bool) throws -> SCPreferences {
        if write { try authorize() }
        guard let p = SCPreferencesCreateWithAuthorization(nil, "BlueGrass.ProxySettings" as CFString, nil, authorization) else {
            throw ProxyFailure.recoveryRequired
        }
        try lifetime.check()
        return p
    }
    private func snapshot(_ preferences: SCPreferences) throws -> ProxySnapshot {
        guard let all = SCNetworkServiceCopyAll(preferences) as? [SCNetworkService],
              all.count <= 256 else { throw ProxyFailure.recoveryRequired }
        let current = SCNetworkSetCopyCurrent(preferences)
        let selected: [SCNetworkService]
        if let current = current { selected = (SCNetworkSetCopyServices(current) as? [SCNetworkService]) ?? [] }
        else { selected = [] }
        let active = selected.filter {
            guard SCNetworkServiceGetEnabled($0),
                  let p = SCNetworkServiceCopyProtocol($0, kSCNetworkProtocolTypeProxies) else { return false }
            return SCNetworkProtocolGetEnabled(p)
        }.compactMap { service -> String? in
            guard let identifier = SCNetworkServiceGetServiceID(service) else { return nil }
            return identifier as String
        }.sorted()
        var services: [String: [String: JSONValue]] = [:]
        for service in all {
            guard let identifier = SCNetworkServiceGetServiceID(service),
                  let p = SCNetworkServiceCopyProtocol(service, kSCNetworkProtocolTypeProxies) else { continue }
            let config = (SCNetworkProtocolGetConfiguration(p) as? [String: Any]) ?? [:]
            let subset = config.filter { ProxyPatch.readable.contains($0.key) }
            guard JSONSerialization.isValidJSONObject(subset) else { throw ProxyFailure.invalidSettings }
            services[identifier as String] = try JSONDecoder().decode([String: JSONValue].self,
                from: JSONSerialization.data(withJSONObject: subset, options: [.sortedKeys]))
        }
        return ProxySnapshot(active: active, services: services)
    }
    func read() throws -> ProxySnapshot {
        let p = try session(write: false)
        SCPreferencesSynchronize(p)
        try lifetime.check()
        return try snapshot(p)
    }
    func compareAndSwap(expected: ProxySnapshot, desired: ProxySnapshot) throws {
        let p = try session(write: true)
        guard SCPreferencesLock(p, false) else { throw ProxyFailure.recoveryRequired }
        defer { SCPreferencesUnlock(p) }
        try lifetime.check()
        SCPreferencesSynchronize(p)
        let changes = try ProxyPatch.plan(current: snapshot(p), expected: expected, desired: desired)
        // Stage every service in the same SCPreferences session. A failure before
        // CommitChanges only discards memory; a later failure leaves Go's durable
        // prepared journal in place for ownership-checked rollback/recovery.
        for (id, keys) in changes {
            try lifetime.check()
            guard let service = SCNetworkServiceCopy(p, id as CFString),
                  let proto = SCNetworkServiceCopyProtocol(service, kSCNetworkProtocolTypeProxies),
                  let after = desired.services[id] else { throw ProxyFailure.recoveryRequired }
            var config = (SCNetworkProtocolGetConfiguration(proto) as? [String: Any]) ?? [:]
            for key in keys {
                if let value = after[key] { config[key] = value.foundation }
                else { config.removeValue(forKey: key) }
            }
            guard SCNetworkProtocolSetConfiguration(proto, config as CFDictionary) else { throw ProxyFailure.recoveryRequired }
        }
        try lifetime.check()
        guard SCPreferencesCommitChanges(p) else { throw ProxyFailure.recoveryRequired }
        try lifetime.check()
        guard SCPreferencesApplyChanges(p) else { throw ProxyFailure.recoveryRequired }
        try lifetime.check()
        guard try snapshot(p) == desired else { throw ProxyFailure.verificationFailed }
    }
}

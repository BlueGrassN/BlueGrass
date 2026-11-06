import Foundation

public enum ProxyFailure: String, Error {
    case ownershipLost = "OWNERSHIP_LOST"
    case recoveryRequired = "RECOVERY_REQUIRED"
    case invalidSettings = "INVALID_SETTINGS"
    case invalidRecord = "INVALID_RECORD"
    case verificationFailed = "SETTINGS_VERIFICATION_FAILED"
}

public indirect enum JSONValue: Codable, Equatable {
    case null, bool(Bool), number(Double), string(String)
    case array([JSONValue]), object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let v = try? c.decode(Bool.self) { self = .bool(v) }
        else if let v = try? c.decode(Double.self) { self = .number(v) }
        else if let v = try? c.decode(String.self) { self = .string(v) }
        else if let v = try? c.decode([JSONValue].self) { self = .array(v) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let v): try c.encode(v)
        case .number(let v): try c.encode(v)
        case .string(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .object(let v): try c.encode(v)
        }
    }
    var foundation: Any {
        switch self {
        case .null: return NSNull()
        case .bool(let v): return v
        case .number(let v): return v
        case .string(let v): return v
        case .array(let v): return v.map(\.foundation)
        case .object(let v): return v.mapValues(\.foundation)
        }
    }
}

public struct ProxySnapshot: Codable, Equatable {
    public var active: [String]
    public var services: [String: [String: JSONValue]]
    public init(active: [String], services: [String: [String: JSONValue]]) {
        self.active = active; self.services = services
    }
}

public enum ProxyPatch {
    public static let writable: Set<String> = [
        "HTTPEnable", "HTTPProxy", "HTTPPort", "HTTPSEnable", "HTTPSProxy", "HTTPSPort",
        "SOCKSEnable", "ProxyAutoConfigEnable", "ProxyAutoDiscoveryEnable",
        "ExceptionsList", "ExcludeSimpleHostnames"
    ]
    // PAC URLs are read for ownership detection but never changed or logged.
    public static let readable = writable.union(["ProxyAutoConfigURLString", "SOCKSProxy", "SOCKSPort"])
    private static let flags: Set<String> = [
        "HTTPEnable", "HTTPSEnable", "SOCKSEnable", "ProxyAutoConfigEnable",
        "ProxyAutoDiscoveryEnable", "ExcludeSimpleHostnames"
    ]

    public static func plan(current: ProxySnapshot, expected: ProxySnapshot,
                            desired: ProxySnapshot) throws -> [String: Set<String>] {
        guard current == expected else { throw ProxyFailure.ownershipLost }
        guard expected.active == desired.active,
              Set(expected.services.keys) == Set(desired.services.keys),
              desired.services.count <= 256 else { throw ProxyFailure.invalidSettings }
        var changes: [String: Set<String>] = [:]
        for (id, before) in expected.services {
            guard !id.isEmpty, id.utf8.count <= 256, let after = desired.services[id],
                  Set(after.keys).isSubset(of: readable) else { throw ProxyFailure.invalidSettings }
            let differing = Set(before.keys).union(after.keys).filter { before[$0] != after[$0] }
            guard differing.isSubset(of: writable) else { throw ProxyFailure.invalidSettings }
            for key in differing {
                if let value = after[key] { try validate(key: key, value: value) }
            }
            if !differing.isEmpty { changes[id] = differing }
        }
        return changes
    }
    private static func validate(key: String, value: JSONValue) throws {
        switch (key, value) {
        case ("HTTPProxy", .string(let s)), ("HTTPSProxy", .string(let s)):
            guard s.utf8.count <= 4096, !s.contains("\0") else { throw ProxyFailure.invalidSettings }
        case ("ExceptionsList", .array(let values)):
            guard values.count <= 512 else { throw ProxyFailure.invalidSettings }
            for v in values {
                guard case .string(let s) = v, s.utf8.count <= 4096,
                      !s.contains("\0") else { throw ProxyFailure.invalidSettings }
            }
        case ("HTTPPort", .number(let n)), ("HTTPSPort", .number(let n)):
            guard n.isFinite, n.rounded() == n, n >= 0, n <= 65535 else { throw ProxyFailure.invalidSettings }
        case (let name, .number(let n)) where flags.contains(name):
            guard n == 0 || n == 1 else { throw ProxyFailure.invalidSettings }
        case (let name, .bool) where flags.contains(name): break // Older CFBoolean preferences.
        default: throw ProxyFailure.invalidSettings
        }
    }
}

public struct ProxyRequest {
    public let operation: String
    public let timeoutMillis: Int64
    public let record: Data?
    public let expected: ProxySnapshot?
    public let desired: ProxySnapshot?

    public static func parse(_ data: Data) throws -> ProxyRequest {
        guard data.count < 1024 * 1024,
              let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let op = object["operation"] as? String,
              let timeout = object["timeoutMillis"] as? Int64,
              timeout > 0, timeout <= 24_000 else { throw ProxyFailure.invalidSettings }
        let allowed: Set<String>
        switch op {
        case "read", "load", "delete": allowed = ["operation", "timeoutMillis"]
        case "save": allowed = ["operation", "timeoutMillis", "record"]
        case "cas": allowed = ["operation", "timeoutMillis", "expected", "desired"]
        default: throw ProxyFailure.invalidSettings
        }
        guard Set(object.keys) == allowed else { throw ProxyFailure.invalidSettings }
        func snapshot(_ name: String) throws -> ProxySnapshot? {
            guard let value = object[name] else { return nil }
            return try JSONDecoder().decode(ProxySnapshot.self, from: JSONSerialization.data(withJSONObject: value))
        }
        var record: Data?
        if let value = object["record"] {
            record = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
            try validateRecord(record!)
        }
        return ProxyRequest(operation: op, timeoutMillis: timeout, record: record,
                            expected: try snapshot("expected"), desired: try snapshot("desired"))
    }
    static func validateRecord(_ data: Data) throws {
        guard data.count <= 512 * 1024,
              let value = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              value["version"] as? Int == 1,
              let phase = value["phase"] as? String,
              ["prepared", "applied", "restoring"].contains(phase),
              let entries = value["entries"] as? [String: Any],
              !entries.isEmpty, entries.count <= 256 else { throw ProxyFailure.invalidRecord }
    }
}

public enum ProxyProtocol {
    public static func failure(_ error: Error) -> Data {
        let code = (error as? ProxyFailure)?.rawValue ?? ProxyFailure.recoveryRequired.rawValue
        // The only diagnostic emitted by the helper is a fixed safe code.
        return Data("{\"ok\":false,\"error\":\"\(code)\"}".utf8)
    }
}

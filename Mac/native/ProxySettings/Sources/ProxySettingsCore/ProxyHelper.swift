import Foundation

public final class ProxyHelper {
    private let processLock: HelperLock
    private let preferences: SystemPreferences
    private let journal: KeychainJournal
    private let lifetime: OperationLifetime

    public init(lockDirectory: String, lifetime: OperationLifetime) throws {
        self.lifetime = lifetime
        processLock = try HelperLock(directory: lockDirectory)
        preferences = try SystemPreferences(lifetime: lifetime)
        journal = KeychainJournal(lifetime: lifetime)
    }
    public func respond(_ data: Data) -> Data {
        do {
            let request = try ProxyRequest.parse(data)
            try lifetime.begin(milliseconds: request.timeoutMillis)
            defer { lifetime.end() }
            var response: [String: Any] = ["ok": true]
            switch request.operation {
            case "read":
                response["snapshot"] = try JSONSerialization.jsonObject(with: JSONEncoder().encode(preferences.read()))
            case "load":
                if let value = try journal.load() { response["record"] = try JSONSerialization.jsonObject(with: value) }
            case "save":
                guard let record = request.record else { throw ProxyFailure.invalidRecord }
                try journal.save(record)
            case "delete": try journal.delete()
            case "cas":
                guard let expected = request.expected, let desired = request.desired else { throw ProxyFailure.invalidSettings }
                try preferences.compareAndSwap(expected: expected, desired: desired)
            default: throw ProxyFailure.invalidSettings
            }
            let result = try JSONSerialization.data(withJSONObject: response, options: [.sortedKeys])
            try lifetime.check()
            guard result.count < 1024 * 1024 else { throw ProxyFailure.recoveryRequired }
            return result
        } catch { return ProxyProtocol.failure(error) }
    }
}

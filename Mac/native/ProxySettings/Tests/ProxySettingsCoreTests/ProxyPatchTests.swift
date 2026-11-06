import XCTest
@testable import ProxySettingsCore

final class ProxyPatchTests: XCTestCase {
    // Pure policy tests: none instantiate the SystemConfiguration or Keychain
    // adapters, request authorization, or modify the CI runner's network.
    func testStaleSnapshotCannotProduceWrites() throws {
        let expected = ProxySnapshot(active: ["wifi"], services: ["wifi": ["HTTPProxy": .string("original")]])
        let current = ProxySnapshot(active: ["wifi"], services: ["wifi": ["HTTPProxy": .string("competitor")]])
        let desired = ProxySnapshot(active: ["wifi"], services: ["wifi": ["HTTPProxy": .string("127.0.0.1")]])
        XCTAssertThrowsError(try ProxyPatch.plan(current: current, expected: expected, desired: desired)) {
            XCTAssertEqual($0 as? ProxyFailure, .ownershipLost)
        }
    }
    func testOnlyChangedProxyFieldsArePatched() throws {
        let before = ProxySnapshot(active: ["wifi"], services: ["wifi": [
            "HTTPEnable": .number(0), "ProxyAutoConfigURLString": .string("https://private/pac")
        ]])
        let after = ProxySnapshot(active: ["wifi"], services: ["wifi": [
            "HTTPEnable": .number(1), "HTTPProxy": .string("127.0.0.1"),
            "ProxyAutoConfigURLString": .string("https://private/pac")
        ]])
        let plan = try ProxyPatch.plan(current: before, expected: before, desired: after)
        XCTAssertEqual(try XCTUnwrap(plan["wifi"]), Set(["HTTPEnable", "HTTPProxy"]))
        // A restore must distinguish absent keys from explicit zero values.
        let reverse = try ProxyPatch.plan(current: after, expected: after, desired: before)
        XCTAssertEqual(try XCTUnwrap(reverse["wifi"]), Set(["HTTPEnable", "HTTPProxy"]))
    }
    func testCannotWriteOtherPreferencesOrChangeServiceSelection() {
        let before = ProxySnapshot(active: ["wifi"], services: ["wifi": [:]])
        for after in [
            ProxySnapshot(active: ["wifi"], services: ["wifi": ["DNS": .string("unrelated")]]),
            ProxySnapshot(active: [], services: ["wifi": [:]]),
            ProxySnapshot(active: ["wifi"], services: ["other-service": [:]]),
            ProxySnapshot(active: ["wifi"], services: ["wifi": ["ProxyAutoConfigURLString": .string("https://other")]])
        ] {
            XCTAssertThrowsError(try ProxyPatch.plan(current: before, expected: before, desired: after)) {
                XCTAssertEqual($0 as? ProxyFailure, .invalidSettings)
            }
        }
    }
    func testStrictIPCRejectsUnknownOrConflictingParameters() {
        for line in [
            #"{"operation":"shell","timeoutMillis":1000,"command":"anything"}"#,
            #"{"operation":"read","timeoutMillis":1000,"record":{}}"#,
            #"{"operation":"cas","timeoutMillis":1000}"#,
            #"{"operation":"read","timeoutMillis":1000,"secret":"must-not-appear"}"#
        ] {
            XCTAssertThrowsError(try ProxyRequest.parse(Data(line.utf8)))
        }
    }
    func testProxyFieldsRejectWrongTypesAndOutOfRangePorts() {
        let before = ProxySnapshot(active: ["wifi"], services: ["wifi": [:]])
        let cases: [[String: JSONValue]] = [
            ["HTTPProxy": .number(123)], ["HTTPPort": .number(65536)],
            ["HTTPEnable": .number(2)], ["ExceptionsList": .bool(true)]
        ]
        for invalid in cases {
            let desired = ProxySnapshot(active: ["wifi"], services: ["wifi": invalid])
            XCTAssertThrowsError(try ProxyPatch.plan(current: before, expected: before, desired: desired))
        }
    }
    func testSafeFailureEnvelopeNeverContainsUnderlyingSecrets() throws {
        let data = ProxyProtocol.failure(ProxyFailure.ownershipLost)
        let value = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(value["error"] as? String, "OWNERSHIP_LOST")
        XCTAssertEqual(value["ok"] as? Bool, false)
        let unknown = ProxyProtocol.failure(NSError(domain: "secret-token", code: 99))
        XCTAssertFalse(String(decoding: unknown, as: UTF8.self).contains("secret-token"))
    }
}

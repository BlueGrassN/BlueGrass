import XCTest
@testable import ProxySettingsCore

final class OperationLifetimeTests: XCTestCase {
    func testOnlyBoundedRequestsAreAccepted() throws {
        let request = try ProxyRequest.parse(Data(#"{"operation":"read","timeoutMillis":1000}"#.utf8))
        let lifetime = OperationLifetime(isParentAlive: { true })
        try lifetime.begin(milliseconds: request.timeoutMillis)
        try lifetime.check()
        for milliseconds in [0, 24_001] {
            XCTAssertThrowsError(try ProxyRequest.parse(Data("{\"operation\":\"read\",\"timeoutMillis\":\(milliseconds)}".utf8)))
        }
    }
    func testLateAuthorizationCannotCommitAfterParentDeath() throws {
        var alive = true
        let lifetime = OperationLifetime(isParentAlive: { alive })
        try lifetime.begin(milliseconds: 1000)
        var committed = false
        XCTAssertThrowsError(try lifetime.afterWait({ alive = false }, then: { committed = true }))
        XCTAssertFalse(committed)
    }
    func testLateKeychainCannotCommitAfterTransactionDeadline() throws {
        var now: UInt64 = 0
        let lifetime = OperationLifetime(isParentAlive: { true }, now: { now })
        try lifetime.begin(milliseconds: 10)
        var committed = false
        XCTAssertThrowsError(try lifetime.afterWait({ now = 11_000_000 }, then: { committed = true }))
        XCTAssertFalse(committed)
    }
    func testCancellationRemainsLatchedAcrossRequests() throws {
        let lifetime = OperationLifetime(isParentAlive: { true })
        try lifetime.begin(milliseconds: 1000)
        lifetime.cancel()
        XCTAssertThrowsError(try lifetime.check())
        XCTAssertThrowsError(try lifetime.begin(milliseconds: 1000))
    }
}

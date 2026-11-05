import XCTest
import Darwin
@testable import ProxySettingsCore

final class ParentDeathMonitorTests: XCTestCase {
    func testClosedParentCancellationPipeStopsBlockedAuthorization() throws {
        var descriptors: [Int32] = [0, 0]
        XCTAssertEqual(pipe(&descriptors), 0)
        let stopped = expectation(description: "parent cancellation observed on independent queue")
        let lifetime = OperationLifetime(isParentAlive: { true })
        let monitor = try ParentDeathMonitor(parent: getppid(), cancellationFD: descriptors[0],
                                             lifetime: lifetime, terminate: { stopped.fulfill() })
        try lifetime.begin(milliseconds: 1000)
        // This stands in for a killed Go parent while the main helper thread is
        // waiting for authorization. No OS preferences or Keychain are touched.
        close(descriptors[1])
        wait(for: [stopped], timeout: 1)
        XCTAssertThrowsError(try lifetime.check())
        withExtendedLifetime(monitor) {}
    }
    func testWatchdogTerminatesExpiredBlockedOperation() throws {
        var descriptors: [Int32] = [0, 0]
        XCTAssertEqual(pipe(&descriptors), 0)
        defer { close(descriptors[1]) }
        let stopped = expectation(description: "deadline terminates blocked helper")
        let lifetime = OperationLifetime(isParentAlive: { true })
        let monitor = try ParentDeathMonitor(parent: getppid(), cancellationFD: descriptors[0],
                                             lifetime: lifetime, terminate: { stopped.fulfill() })
        try lifetime.begin(milliseconds: 10)
        wait(for: [stopped], timeout: 1)
        XCTAssertThrowsError(try lifetime.check())
        withExtendedLifetime(monitor) {}
    }
}

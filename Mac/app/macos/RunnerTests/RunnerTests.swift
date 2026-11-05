import Cocoa
import Security
import UserNotifications
import XCTest

// Unhosted logic tests: selected native sources are compiled into RunnerTests.
// Never launch Flutter, read the user's Keychain, register login items, or
// request real notification permission during xcodebuild test.

final class RunnerTests: XCTestCase {
  func testRecordKeysCannotEscapeNamespaceAndValuesHaveByteLimits() throws {
    for key in ["", "../session", "a/b", "账户", String(repeating: "a", count: 101)] {
      XCTAssertThrowsError(try PlatformValidation.recordKey(key))
    }
    XCTAssertEqual(try PlatformValidation.recordKey("session_v1-ABC"), "session_v1-ABC")
    XCTAssertNoThrow(try PlatformValidation.recordValue(String(repeating: "x", count: 1_048_576)))
    XCTAssertThrowsError(try PlatformValidation.recordValue(String(repeating: "é", count: 524_289)))
  }

  func testURLLaunchRejectsExecutableSchemesCredentialsAndMalformedURLs() throws {
    for url in ["file:///tmp/a", "http://example.com", "javascript:alert(1)",
                "https://user:password@example.com", "https:///path", "https://example.com\n",
                "https://example.com/%0a", "https://example.com:0", "https://example.com:65536"] {
      XCTAssertThrowsError(try PlatformValidation.externalURL(url), url)
    }
    XCTAssertEqual(try PlatformValidation.externalURL("https://example.com/a?b=c").host, "example.com")
  }

  func testWireTypesDoNotCoerceNumbersToBooleansOrFractionalColors() throws {
    XCTAssertThrowsError(try PlatformArguments(["enabled": 1]).bool("enabled"))
    XCTAssertThrowsError(try PlatformArguments(["caption": true]).argb("caption"))
    XCTAssertThrowsError(try PlatformArguments(["caption": 1.5]).argb("caption"))
    XCTAssertThrowsError(try PlatformArguments(["caption": -1]).argb("caption"))
    XCTAssertTrue(try PlatformArguments(["enabled": true]).bool("enabled"))
    XCTAssertEqual(try PlatformArguments(["caption": Int64(0xFFAABBCC)]).argb("caption"), 0xFFAABBCC)
  }

  func testExportOnlyAcceptsLeafNamesAndBoundedContent() throws {
    for name in ["", ".", "..", "../secrets", "/tmp/a", "a:b", "a\\b", "a\n.json"] {
      XCTAssertThrowsError(try PlatformValidation.exportName(name))
    }
    XCTAssertEqual(try PlatformValidation.exportName("BlueGrass-诊断.json"), "BlueGrass-诊断.json")
    XCTAssertThrowsError(try PlatformValidation.exportContent(String(repeating: "x", count: 8_388_609)))
  }

  func testQuitRequiresDartAcknowledgementAndFailedDisconnectCanRetry() {
    let gate = ExitHandshake()
    XCTAssertFalse(gate.mayTerminate)
    XCTAssertTrue(gate.beginRequest())
    XCTAssertFalse(gate.beginRequest())
    gate.requestReturned()
    XCTAssertFalse(gate.mayTerminate)
    XCTAssertTrue(gate.beginRequest())
    gate.acknowledgeCleanup()
    gate.requestReturned()
    XCTAssertTrue(gate.mayTerminate)
    XCTAssertFalse(gate.beginRequest())
  }

  func testColdNotificationActivationsAreBoundedDeduplicatedAndCancellable() {
    var queue = NotificationActivationQueue()
    queue.append("a")
    queue.append("a")
    queue.append("b")
    queue.remove("a")
    XCTAssertEqual(queue.take(), "b")
    XCTAssertNil(queue.take())
    for index in 0..<40 { queue.append("notice-\(index)") }
    XCTAssertEqual(queue.take(), "notice-8")
  }

  func testFailedKeychainReplacementPreservesPreviousRecordAndMissingDeleteIsOK() throws {
    let backend = MemoryKeychain()
    let records = KeychainRecordStore(backend: backend)
    XCTAssertNil(try records.read("session"))
    try records.write("session", value: "old-token")
    backend.failUpdates = true
    XCTAssertThrowsError(try records.write("session", value: "new-token"))
    XCTAssertEqual(try records.read("session"), "old-token")
    backend.failUpdates = false
    try records.write("session", value: "new-token")
    XCTAssertEqual(try records.read("session"), "new-token")
    try records.delete("session")
    try records.delete("session")
    XCTAssertNil(try records.read("session"))
  }

  func testConcurrentKeychainInsertRetriesDuplicateWithoutDeleting() throws {
    let backend = MemoryKeychain()
    backend.raceOnAdd = true
    let records = KeychainRecordStore(backend: backend)
    try records.write("session", value: "replacement")
    XCTAssertEqual(try records.read("session"), "replacement")
    XCTAssertEqual(backend.deleteCount, 0)
  }

  func testCorruptKeychainDataIsAnErrorRatherThanAnEmptySession() {
    let backend = MemoryKeychain()
    backend.values["session"] = Data([0xFF])
    XCTAssertThrowsError(try KeychainRecordStore(backend: backend).read("session"))
  }

  func testExportReplacesSymlinkWithoutOverwritingItsTargetAndKeepsFilePrivate() throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    defer { try? FileManager.default.removeItem(at: directory) }
    let target = directory.appendingPathComponent("original")
    let link = directory.appendingPathComponent("export.json")
    try Data("untouched".utf8).write(to: target)
    try FileManager.default.createSymbolicLink(at: link, withDestinationURL: target)
    try AtomicFileExport.write(Data("report".utf8), to: link)
    XCTAssertEqual(try String(contentsOf: link, encoding: .utf8), "report")
    XCTAssertEqual(try String(contentsOf: target, encoding: .utf8), "untouched")
    let attributes = try FileManager.default.attributesOfItem(atPath: link.path)
    XCTAssertEqual((attributes[.posixPermissions] as? NSNumber)?.intValue, 0o600)
    XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 2)
  }

  func testFailedExportPreservesExistingDestinationAndCleansTemporaryFiles() throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    defer { try? FileManager.default.removeItem(at: directory) }
    let destination = directory.appendingPathComponent("existing-directory")
    try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: false)
    XCTAssertThrowsError(try AtomicFileExport.write(Data("report".utf8), to: destination))
    XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path), ["existing-directory"])
  }

  func testCancellingWhileNotificationPermissionIsPendingDoesNotPublishAfterApproval() {
    let backend = ControlledNotifications()
    let notices = ExpiryNotifications(backend: backend)
    let submitted = expectation(description: "cancelled submission resolves false")
    let cancelled = expectation(description: "cancel resolves")
    notices.submit(id: "batch-1", title: "BlueGrass", body: "Expiry") { accepted in
      XCTAssertFalse(accepted)
      submitted.fulfill()
    }
    notices.cancel(id: "batch-1") { accepted in
      XCTAssertTrue(accepted)
      cancelled.fulfill()
    }
    backend.authorization?(true, nil)
    wait(for: [submitted, cancelled], timeout: 2)
    XCTAssertTrue(backend.added.isEmpty)
  }

  func testNotificationPermissionDenialReturnsFalseWithoutPublishing() {
    let backend = ControlledNotifications()
    let notices = ExpiryNotifications(backend: backend)
    let finished = expectation(description: "denied")
    notices.submit(id: "batch-1", title: "BlueGrass", body: "Expiry") { accepted in
      XCTAssertFalse(accepted)
      finished.fulfill()
    }
    backend.authorization?(false, nil)
    wait(for: [finished], timeout: 2)
    XCTAssertTrue(backend.added.isEmpty)
  }
}

private final class ControlledNotifications: ExpiryNotificationBackend {
  var authorization: ((Bool, Error?) -> Void)?
  var added: [UNNotificationRequest] = []
  func setDelegate(_ delegate: UNUserNotificationCenterDelegate) {}
  func requestAuthorization(completion: @escaping (Bool, Error?) -> Void) { authorization = completion }
  func add(_ request: UNNotificationRequest, completion: @escaping (Error?) -> Void) {
    added.append(request)
    completion(nil)
  }
  func remove(_ identifier: String) {}
}

// Real Keychain failures cannot be induced deterministically in unit tests.
private final class MemoryKeychain: KeychainBackend {
  var values: [String: Data] = [:]
  var failUpdates = false
  var raceOnAdd = false
  var deleteCount = 0
  private func key(_ query: [String: Any]) -> String { query[kSecAttrAccount as String] as! String }
  func read(_ query: [String: Any]) -> (OSStatus, Data?) {
    guard let value = values[key(query)] else { return (errSecItemNotFound, nil) }
    return (errSecSuccess, value)
  }
  func update(_ query: [String: Any], attributes: [String: Any]) -> OSStatus {
    if failUpdates { return errSecAuthFailed }
    guard values[key(query)] != nil else { return errSecItemNotFound }
    values[key(query)] = attributes[kSecValueData as String] as? Data
    return errSecSuccess
  }
  func add(_ attributes: [String: Any]) -> OSStatus {
    if raceOnAdd {
      raceOnAdd = false
      values[key(attributes)] = Data("racing-writer".utf8)
      return errSecDuplicateItem
    }
    guard values[key(attributes)] == nil else { return errSecDuplicateItem }
    values[key(attributes)] = attributes[kSecValueData as String] as? Data
    return errSecSuccess
  }
  func delete(_ query: [String: Any]) -> OSStatus {
    deleteCount += 1
    return values.removeValue(forKey: key(query)) == nil ? errSecItemNotFound : errSecSuccess
  }
}

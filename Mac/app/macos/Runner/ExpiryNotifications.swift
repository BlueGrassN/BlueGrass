import Cocoa
import UserNotifications

protocol ExpiryNotificationBackend {
  func setDelegate(_ delegate: UNUserNotificationCenterDelegate)
  func requestAuthorization(completion: @escaping (Bool, Error?) -> Void)
  func add(_ request: UNNotificationRequest, completion: @escaping (Error?) -> Void)
  func remove(_ identifier: String)
}

struct SystemExpiryNotificationBackend: ExpiryNotificationBackend {
  private let center = UNUserNotificationCenter.current()
  func setDelegate(_ delegate: UNUserNotificationCenterDelegate) { center.delegate = delegate }
  func requestAuthorization(completion: @escaping (Bool, Error?) -> Void) {
    center.requestAuthorization(options: [.alert, .sound], completionHandler: completion)
  }
  func add(_ request: UNNotificationRequest, completion: @escaping (Error?) -> Void) {
    center.add(request, withCompletionHandler: completion)
  }
  func remove(_ identifier: String) {
    center.removePendingNotificationRequests(withIdentifiers: [identifier])
    center.removeDeliveredNotifications(withIdentifiers: [identifier])
  }
}

final class ExpiryNotifications: NSObject, UNUserNotificationCenterDelegate {
  private let backend: ExpiryNotificationBackend
  private let prefix = "com.sunipip.bluegrass.macos.expiry."
  private var activations = NotificationActivationQueue()
  private var operations: [(@escaping () -> Void) -> Void] = []
  private var running = false
  private var submissions: [UUID: String] = [:]
  private var cancelled = Set<UUID>()
  var activationAvailable: (() -> Void)?

  init(backend: ExpiryNotificationBackend = SystemExpiryNotificationBackend()) {
    self.backend = backend
    super.init()
    backend.setDelegate(self)
  }

  // Serialize authorization/add/cancel so an account switch cannot cancel a
  // pending authorization and then accidentally publish the old notice later.
  private func enqueue(_ operation: @escaping (@escaping () -> Void) -> Void) -> Bool {
    guard operations.count < 64 else { return false }
    operations.append(operation)
    runNext()
    return true
  }

  private func runNext() {
    guard !running, let operation = operations.first else { return }
    running = true
    operation { [weak self] in
      DispatchQueue.main.async {
        guard let self = self else { return }
        self.operations.removeFirst()
        self.running = false
        self.runNext()
      }
    }
  }

  func submit(id: String, title: String, body: String, completion: @escaping (Bool) -> Void) {
    let token = UUID()
    submissions[token] = id
    let accepted = enqueue { [self] done in
      backend.requestAuthorization { [self] granted, error in
        DispatchQueue.main.async { [self] in
          guard granted, error == nil, !cancelled.contains(token) else {
            submissions.removeValue(forKey: token)
            cancelled.remove(token)
            completion(false)
            done()
            return
          }
          let content = UNMutableNotificationContent()
          content.title = title
          content.body = body
          content.sound = .default
          let identifier = prefix + id
          let request = UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
          // Replacing the same stable ledger ID does not accumulate banners.
          backend.remove(identifier)
          backend.add(request) { [self] error in
            DispatchQueue.main.async { [self] in
              let wasCancelled = cancelled.remove(token) != nil
              submissions.removeValue(forKey: token)
              if wasCancelled {
                backend.remove(identifier)
              }
              completion(error == nil && !wasCancelled)
              done()
            }
          }
        }
      }
    }
    if !accepted { submissions.removeValue(forKey: token); completion(false) }
  }

  func cancel(id: String, completion: @escaping (Bool) -> Void) {
    activations.remove(id)
    for (token, pendingID) in submissions where pendingID == id { cancelled.insert(token) }
    let accepted = enqueue { [self] done in
      let identifier = prefix + id
      backend.remove(identifier)
      activations.remove(id)
      completion(true)
      done()
    }
    if !accepted { completion(false) }
  }

  func takeActivation() -> String? { activations.take() }

  func userNotificationCenter(_ center: UNUserNotificationCenter,
                              willPresent notification: UNNotification,
                              withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
    completionHandler([.banner, .list, .sound])
  }

  func userNotificationCenter(_ center: UNUserNotificationCenter,
                              didReceive response: UNNotificationResponse,
                              withCompletionHandler completionHandler: @escaping () -> Void) {
    let identifier = response.notification.request.identifier
    guard response.actionIdentifier == UNNotificationDefaultActionIdentifier,
          identifier.hasPrefix(prefix),
          let id = try? PlatformValidation.notificationID(String(identifier.dropFirst(prefix.count))) else {
      completionHandler()
      return
    }
    DispatchQueue.main.async { [weak self] in
      self?.activations.append(id)
      self?.activationAvailable?()
      completionHandler()
    }
  }
}

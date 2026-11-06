import Cocoa
import FlutterMacOS
import ServiceManagement
import UniformTypeIdentifiers

final class BlueGrassPlatform: NSObject {
  private let channel: FlutterMethodChannel
  private weak var window: NSWindow?
  private let records = KeychainRecordStore()
  private let worker = DispatchQueue(label: "com.sunipip.bluegrass.macos.records", qos: .userInitiated)
  private let notifications = ExpiryNotifications()
  let exitHandshake = ExitHandshake()
  private var statusItem: NSStatusItem?
  private var statusLabel: NSMenuItem?
  private var savePanel: NSSavePanel?
  private var startupBusy = false
  private var pendingRecordOperations = 0

  init(messenger: FlutterBinaryMessenger, window: NSWindow) {
    self.channel = FlutterMethodChannel(name: "bluegrass/platform", binaryMessenger: messenger)
    self.window = window
    super.init()
    channel.setMethodCallHandler { [weak self] call, result in
      guard let self = self else {
        result(FlutterError(code: "host_unavailable", message: "Native host unavailable.", details: nil))
        return
      }
      self.handle(call, result: result)
    }
    notifications.activationAvailable = { [weak self] in
      self?.showWindow()
      // Dart drains the retained queue after installing its handler, including
      // clicks received while the engine was still starting.
      self?.channel.invokeMethod("expiryNotificationAvailable", arguments: nil)
    }
    installStatusItem()
  }

  private func failure(_ error: Error) -> FlutterError {
    let code = (error as? PlatformFailure ?? .unsupportedCapability).rawValue
    return FlutterError(code: code, message: "macOS operation failed (\(code)).", details: nil)
  }

  private func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    do {
      switch call.method {
      case "readRecord", "writeRecord", "deleteRecord":
        guard pendingRecordOperations < 32 else { throw PlatformFailure.busy }
        let args = try PlatformArguments(call.arguments)
        let key = try PlatformValidation.recordKey(args.string("key", maxBytes: 100))
        let value = call.method == "writeRecord"
          ? try args.string("value", maxBytes: PlatformValidation.maxRecordBytes, allowEmpty: true) : nil
        pendingRecordOperations += 1
        worker.async { [self] in
          do {
            let response: String?
            switch call.method {
            case "readRecord": response = try records.read(key)
            case "writeRecord": try records.write(key, value: value!); response = nil
            default: try records.delete(key); response = nil
            }
            DispatchQueue.main.async { self.pendingRecordOperations -= 1; result(response) }
          } catch {
            let response = failure(error)
            DispatchQueue.main.async { self.pendingRecordOperations -= 1; result(response) }
          }
        }
      case "openUrl":
        let args = try PlatformArguments(call.arguments)
        let url = try PlatformValidation.externalURL(args.string("url", maxBytes: 4096))
        guard NSWorkspace.shared.open(url) else { throw PlatformFailure.openURLFailed }
        result(nil)
      case "setWindowChrome":
        let args = try PlatformArguments(call.arguments)
        let caption = try args.argb("caption")
        _ = try args.argb("text") // AppKit owns accessible title text rendering.
        let dark = try args.bool("dark")
        guard let window = window else { throw PlatformFailure.windowUnavailable }
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.titlebarAppearsTransparent = true
        window.backgroundColor = NSColor(srgbRed: CGFloat((caption >> 16) & 255) / 255,
                                         green: CGFloat((caption >> 8) & 255) / 255,
                                         blue: CGFloat(caption & 255) / 255, alpha: 1)
        result(nil)
      case "setTrayState":
        let args = try PlatformArguments(call.arguments)
        let label = try args.string("label", maxBytes: 512)
        guard !label.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) else {
          throw PlatformFailure.invalidArguments
        }
        statusLabel?.title = label
        statusItem?.button?.toolTip = "BlueGrass · \(label)"
        result(nil)
      case "getAutostart": result(SMAppService.mainApp.status == .enabled)
      case "getAutostartStatus": result(startupStatus())
      case "setAutostart":
        try setAutostart(try PlatformArguments(call.arguments).bool("enabled"), result: result)
      case "submitExpiryNotification":
        let args = try PlatformArguments(call.arguments)
        let id = try PlatformValidation.notificationID(args.string("id", maxBytes: 256))
        let title = try args.string("title", maxBytes: 512)
        let body = try args.string("body", maxBytes: 4096, allowEmpty: true)
        notifications.submit(id: id, title: title, body: body) { result($0) }
      case "cancelExpiryNotification":
        let args = try PlatformArguments(call.arguments)
        let id = try PlatformValidation.notificationID(args.string("id", maxBytes: 256))
        notifications.cancel(id: id) { [self] success in
          result(success ? nil : failure(PlatformFailure.busy))
        }
      case "takeExpiryNotificationActivation": result(notifications.takeActivation())
      case "saveFile", "exportFile": try exportFile(call.arguments, result: result)
      case "showWindow": showWindow(); result(nil)
      case "exit":
        // Explicit acknowledgement from Dart, after disconnect/proxy rollback.
        // Do not infer permission from the exitRequested callback's return.
        exitHandshake.acknowledgeCleanup()
        result(nil)
        DispatchQueue.main.async { NSApp.terminate(nil) }
      case "getPlatformCapabilities":
        result(["keychain": true, "nativeSaveDialog": true, "menuBar": true,
                "loginItemAPI": true, "localNotificationAPI": true,
                "tun": false, "automaticUpdater": false, "controlHttp": false])
      case "controlHttpConfigure", "controlHttpSend", "controlHttpCancel",
           "installUpdate", "enableTun":
        throw PlatformFailure.unsupportedCapability
      default: result(FlutterMethodNotImplemented)
      }
    } catch { result(failure(error)) }
  }

  private func startupStatus() -> String {
    switch SMAppService.mainApp.status {
    case .enabled: return "enabled"
    case .notRegistered: return "notRegistered"
    case .requiresApproval: return "requiresApproval"
    case .notFound: return "notFound"
    @unknown default: return "unknown"
    }
  }

  private func setAutostart(_ enabled: Bool, result: @escaping FlutterResult) throws {
    guard !startupBusy else { throw PlatformFailure.busy }
    if enabled {
      if SMAppService.mainApp.status != .enabled {
        do { try SMAppService.mainApp.register() }
        catch { throw PlatformFailure.startupFailed }
      }
      if SMAppService.mainApp.status == .requiresApproval { throw PlatformFailure.startupApprovalRequired }
      guard SMAppService.mainApp.status == .enabled else { throw PlatformFailure.startupFailed }
      result(nil)
    } else {
      guard SMAppService.mainApp.status != .notRegistered else { result(nil); return }
      startupBusy = true
      // The completion variant avoids blocking the UI during service removal.
      SMAppService.mainApp.unregister { [weak self] error in
        DispatchQueue.main.async {
          guard let self = self else { return }
          self.startupBusy = false
          if error != nil || SMAppService.mainApp.status != .notRegistered {
            result(self.failure(PlatformFailure.startupFailed))
          } else { result(nil) }
        }
      }
    }
  }

  private func exportFile(_ arguments: Any?, result: @escaping FlutterResult) throws {
    guard savePanel == nil else { throw PlatformFailure.busy }
    guard let window = window else { throw PlatformFailure.windowUnavailable }
    let args = try PlatformArguments(arguments)
    let name = try PlatformValidation.exportName(args.string("suggestedName", maxBytes: 200))
    let content = try PlatformValidation.exportContent(
      args.string("content", maxBytes: PlatformValidation.maxExportBytes, allowEmpty: true))
    let panel = NSSavePanel()
    panel.title = "Export BlueGrass file"
    panel.nameFieldStringValue = name
    panel.canCreateDirectories = true
    panel.isExtensionHidden = false
    if let type = UTType(filenameExtension: (name as NSString).pathExtension) {
      panel.allowedContentTypes = [type]
      panel.allowsOtherFileTypes = false
    }
    savePanel = panel
    showWindow()
    panel.beginSheetModal(for: window) { [self] response in
      guard response == .OK, let url = panel.url else {
        savePanel = nil
        result(nil) // User cancellation is nullable, never a successful path.
        return
      }
      worker.async { [self] in
        do {
          // An auxiliary file + rename keeps a failed export from truncating
          // the user's existing file. Permission is private to this OS user.
          try AtomicFileExport.write(content, to: url)
          DispatchQueue.main.async { self.savePanel = nil; result(url.path) }
        } catch {
          DispatchQueue.main.async { self.savePanel = nil; result(self.failure(PlatformFailure.exportFailed)) }
        }
      }
    }
  }

  func showWindow() {
    NSApp.unhide(nil)
    window?.deminiaturize(nil)
    window?.makeKeyAndOrderFront(nil)
    NSApp.activate(ignoringOtherApps: true)
  }

  func hideToTray() {
    // A user can remove a status item in system settings. Keep the regular
    // Dock icon and reopen handler, so hiding never removes the way back in.
    window?.orderOut(nil)
  }

  func requestExit() {
    guard exitHandshake.beginRequest() else { return }
    channel.invokeMethod("exitRequested", arguments: nil) { [weak self] _ in
      self?.exitHandshake.requestReturned()
      // Failed/unhandled disconnect leaves the process alive and retryable.
      if self?.exitHandshake.mayTerminate == false { self?.showWindow() }
    }
  }

  @objc private func openFromMenu(_ sender: Any?) { showWindow() }
  @objc private func toggleFromMenu(_ sender: Any?) {
    channel.invokeMethod("toggleConnection", arguments: nil)
  }
  @objc private func quitFromMenu(_ sender: Any?) { requestExit() }

  private func installStatusItem() {
    let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    if let icon = NSApp.applicationIconImage?.copy() as? NSImage {
      icon.size = NSSize(width: 18, height: 18)
      item.button?.image = icon
      item.button?.image?.isTemplate = false
    } else { item.button?.title = "BG" }
    item.button?.toolTip = "BlueGrass"
    let menu = NSMenu()
    let label = NSMenuItem(title: "BlueGrass", action: nil, keyEquivalent: "")
    label.isEnabled = false
    menu.addItem(label)
    menu.addItem(.separator())
    for (title, selector) in [("Open BlueGrass", #selector(openFromMenu(_:))),
                              ("Connect / Disconnect", #selector(toggleFromMenu(_:))),
                              ("Quit BlueGrass", #selector(quitFromMenu(_:)))] {
      let entry = NSMenuItem(title: title, action: selector, keyEquivalent: "")
      entry.target = self
      menu.addItem(entry)
    }
    item.menu = menu
    statusLabel = label
    statusItem = item
  }
}

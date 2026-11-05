import Cocoa
import FlutterMacOS

@main
class AppDelegate: FlutterAppDelegate {
  private var host: BlueGrassPlatform? {
    (mainFlutterWindow as? MainFlutterWindow)?.platformHost
  }

  override func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    return false
  }

  override func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
    guard let host = host else { return .terminateCancel }
    if host.exitHandshake.mayTerminate { return .terminateNow }
    // Cmd-Q, Dock Quit, menu Quit, and system logout all go through Dart.
    // A hung engine is deliberately not a reason to discard proxy cleanup.
    host.requestExit()
    return .terminateCancel
  }

  override func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
    host?.showWindow()
    return true
  }

  override func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
    return true
  }
}

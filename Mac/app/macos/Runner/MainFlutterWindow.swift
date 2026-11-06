import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow, NSWindowDelegate {
  private(set) var platformHost: BlueGrassPlatform?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)

    RegisterGeneratedPlugins(registry: flutterViewController)

    platformHost = BlueGrassPlatform(messenger: flutterViewController.engine.binaryMessenger, window: self)
    title = "BlueGrass"
    setContentSize(NSSize(width: 1040, height: 720))
    contentMinSize = NSSize(width: 900, height: 620)
    isReleasedWhenClosed = false

    super.awakeFromNib()
    delegate = self
  }

  func windowShouldClose(_ sender: NSWindow) -> Bool {
    if platformHost?.exitHandshake.mayTerminate == true { return true }
    platformHost?.hideToTray()
    return false
  }
}

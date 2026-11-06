import Foundation
import Darwin

enum AtomicFileExport {
  static func write(_ data: Data, to destination: URL) throws {
    guard destination.isFileURL, data.count <= PlatformValidation.maxExportBytes else {
      throw PlatformFailure.invalidArguments
    }
    let manager = FileManager.default
    let directory = destination.deletingLastPathComponent()
      .appendingPathComponent(".bluegrass-export-" + UUID().uuidString, isDirectory: true)
    do {
      try manager.createDirectory(at: directory, withIntermediateDirectories: false,
                                  attributes: [.posixPermissions: NSNumber(value: 0o700)])
      defer { try? manager.removeItem(at: directory) }
      let temporary = directory.appendingPathComponent("export")
      try data.write(to: temporary, options: .atomic)
      try manager.setAttributes([.posixPermissions: NSNumber(value: 0o600)], ofItemAtPath: temporary.path)
      // Same directory/filesystem: POSIX rename atomically replaces the final
      // directory entry, including a symlink, without following its target.
      let status = temporary.withUnsafeFileSystemRepresentation { source in
        destination.withUnsafeFileSystemRepresentation { target in
          guard let source = source, let target = target else { return Int32(-1) }
          return rename(source, target)
        }
      }
      guard status == 0 else { throw PlatformFailure.exportFailed }
    } catch { throw PlatformFailure.exportFailed }
  }
}

import Foundation
import Darwin
import ProxySettingsCore

// No CLI arguments, shell commands, or control socket. The Go host owns these
// inherited pipes. Bound each frame before parsing so hostile input cannot grow
// an unbounded readLine() allocation. Stdout contains protocol envelopes only.
guard CommandLine.arguments.count == 1,
      let directory = ProcessInfo.processInfo.environment["BLUEGRASS_PROXY_LOCK_DIRECTORY"],
      let parentText = ProcessInfo.processInfo.environment["BLUEGRASS_PROXY_PARENT_PID"],
      let parent = Int32(parentText), parent > 1, getppid() == parent else { exit(1) }
let lifetime = OperationLifetime(isParentAlive: { getppid() == parent })
let parentMonitor: ParentDeathMonitor
do { parentMonitor = try ParentDeathMonitor(parent: parent, cancellationFD: 3, lifetime: lifetime) }
catch { exit(1) }
let helper: ProxyHelper
do { helper = try ProxyHelper(lockDirectory: directory, lifetime: lifetime) }
catch { exit(1) }

var frame = Data()
let input = FileHandle.standardInput
let output = FileHandle.standardOutput
do {
    while let chunk = try input.read(upToCount: 4096), !chunk.isEmpty {
        for byte in chunk {
            if byte == 10 {
                var response = helper.respond(frame)
                response.append(10)
                try output.write(contentsOf: response)
                frame.removeAll(keepingCapacity: true)
            } else {
                guard frame.count < 1024 * 1024 - 1 else { exit(1) }
                frame.append(byte)
            }
        }
    }
    if !frame.isEmpty { exit(1) }
} catch { exit(1) }

import Foundation
import Dispatch
import Darwin

// This guard is checked after every potentially interactive wait and directly
// before preferences mutation. Its monotonic budget comes from the Go manager's
// remaining whole-transaction deadline, not a new allowance for each RPC.
public final class OperationLifetime {
    private let lock = NSLock()
    private let isParentAlive: () -> Bool
    private let now: () -> UInt64
    private var cancelled = false
    private var deadline: UInt64?

    public init(isParentAlive: @escaping () -> Bool,
                now: @escaping () -> UInt64 = { DispatchTime.now().uptimeNanoseconds }) {
        self.isParentAlive = isParentAlive; self.now = now
    }
    public func begin(milliseconds: Int64) throws {
        guard milliseconds > 0, milliseconds <= 24_000 else { throw ProxyFailure.recoveryRequired }
        try check()
        lock.lock(); deadline = now() + UInt64(milliseconds) * 1_000_000; lock.unlock()
        try check()
    }
    public func end() { lock.lock(); deadline = nil; lock.unlock() }
    public func cancel() { lock.lock(); cancelled = true; lock.unlock() }
    public func check() throws {
        lock.lock(); let stopped = cancelled; let until = deadline; lock.unlock()
        guard !stopped, isParentAlive(), until.map({ now() < $0 }) ?? true else {
            throw ProxyFailure.recoveryRequired
        }
    }
    func afterWait(_ wait: () throws -> Void, then action: () throws -> Void) throws {
        try check(); try wait(); try check(); try action()
    }
}

// Independent queue: authorization/Keychain may block the protocol thread.
// FD 3 is a dedicated cancellation pipe whose only writer belongs to the Go
// parent and is CLOEXEC in all children. Parent death closes it in the kernel.
public final class ParentDeathMonitor {
    private let lifetime: OperationLifetime
    private let terminate: () -> Void
    private let lock = NSLock()
    private var stopped = false
    private let cancellation: DispatchSourceRead
    private let process: DispatchSourceProcess
    private let watchdog: DispatchSourceTimer

    public init(parent: pid_t, cancellationFD: Int32, lifetime: OperationLifetime,
                terminate: @escaping () -> Void = { _exit(1) }) throws {
        guard parent > 1, getppid() == parent, fcntl(cancellationFD, F_GETFD) >= 0 else {
            throw ProxyFailure.recoveryRequired
        }
        self.lifetime = lifetime; self.terminate = terminate
        let queue = DispatchQueue(label: "org.bluegrass.proxy-parent", qos: .userInitiated)
        cancellation = DispatchSource.makeReadSource(fileDescriptor: cancellationFD, queue: queue)
        process = DispatchSource.makeProcessSource(identifier: parent, eventMask: .exit, queue: queue)
        watchdog = DispatchSource.makeTimerSource(queue: queue)
        cancellation.setEventHandler { [weak self] in self?.stop() }
        cancellation.setCancelHandler { close(cancellationFD) }
        process.setEventHandler { [weak self] in self?.stop() }
        watchdog.setEventHandler { [weak self] in
            guard let self = self else { return }
            do { try self.lifetime.check() } catch { self.stop() }
        }
        watchdog.schedule(deadline: .now(), repeating: .milliseconds(50), leeway: .milliseconds(5))
        cancellation.activate(); process.activate(); watchdog.activate()
        // Registration is asynchronous: synchronous liveness plus the pipe and
        // watchdog cover a parent exit before the process source registers.
        do { try lifetime.check() } catch { stop(); throw error }
    }
    private func stop() {
        lock.lock(); if stopped { lock.unlock(); return }; stopped = true; lock.unlock()
        lifetime.cancel()
        cancellation.cancel(); process.cancel(); watchdog.cancel()
        terminate()
    }
    deinit { cancellation.cancel(); process.cancel(); watchdog.cancel() }
}

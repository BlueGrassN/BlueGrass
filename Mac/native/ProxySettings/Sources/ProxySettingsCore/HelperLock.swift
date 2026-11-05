import Foundation
import Darwin

// Independent from the Go lock: an orphaned helper must finish/exit before a new
// host can read or mutate the journal. Locks release automatically on process exit.
final class HelperLock {
    private let fd: Int32
    init(directory: String) throws {
        guard getuid() != 0, directory.hasPrefix("/"), !directory.contains("\0") else { throw ProxyFailure.recoveryRequired }
        var directoryStat = stat()
        guard lstat(directory, &directoryStat) == 0,
              directoryStat.st_mode & mode_t(S_IFMT) == mode_t(S_IFDIR),
              directoryStat.st_uid == getuid(), directoryStat.st_mode & 0o077 == 0 else { throw ProxyFailure.recoveryRequired }
        let descriptor = open(directory + "/proxy-settings.lock", O_CREAT | O_RDWR | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard descriptor >= 0 else { throw ProxyFailure.recoveryRequired }
        var fileStat = stat()
        guard fstat(descriptor, &fileStat) == 0, fileStat.st_uid == getuid(),
              fileStat.st_mode & mode_t(S_IFMT) == mode_t(S_IFREG),
              fileStat.st_mode & 0o077 == 0, fileStat.st_nlink == 1,
              flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            close(descriptor); throw ProxyFailure.recoveryRequired
        }
        fd = descriptor
    }
    deinit { _ = flock(fd, LOCK_UN); close(fd) }
}

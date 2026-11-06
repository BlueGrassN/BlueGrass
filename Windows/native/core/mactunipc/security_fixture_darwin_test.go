//go:build darwin

package mactunipc

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// A durable Run directory can retain both files across reboot. Seed the real
// paths before the signed worker starts; successful subsequent bind/Hello and
// socket exit prove the server recovered the stale node under its existing lock.
func smokeStaleRuntimeFixture(owner Identity) error {
	if _, err := smokeSecurityWitness(owner); err != nil {
		return err
	}
	lock, err := rootFile(canonicalRuntime+"/worker.lock", unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	contender, err := rootFile(canonicalRuntime+"/worker.lock", unix.O_RDWR)
	if err != nil {
		return err
	}
	busy := unix.Flock(int(contender.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	_ = contender.Close()
	if !errors.Is(busy, unix.EWOULDBLOCK) {
		return errors.New("live runtime lease did not exclude another owner")
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: SocketPath, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	if err = listener.Close(); err != nil {
		return err
	}
	if !socketNoACL(SocketPath) {
		return errors.New("stale socket lost root/no-ACL metadata")
	}
	c, err := net.DialTimeout("unix", SocketPath, 200*time.Millisecond)
	if err == nil {
		_ = c.Close()
		return errors.New("stale socket unexpectedly has a live listener")
	}
	return nil
}

func smokeSecurityWitness(owner Identity) (smokeWitness, error) {
	var w smokeWitness
	f, err := rootFile(smokeWitnessPath, unix.O_RDONLY)
	if err != nil {
		return w, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	_ = f.Close()
	if err != nil || len(b) > 16384 || json.Unmarshal(b, &w) != nil || w.Owner != owner || w.Hash != ExpectedWorkerSHA256 {
		return w, errors.New("security fixture ownership witness invalid")
	}
	for _, p := range []string{canonicalRuntime, StateDirectory} {
		id, err := smokeDirectoryIdentity(p)
		if err != nil || w.Directories[p] != id {
			return w, errors.New("security fixture directories replaced")
		}
	}
	id, err := smokeDirectoryIdentity(filepath.Dir(IntentPath))
	if err != nil || w.IntentParent != id {
		return w, errors.New("security fixture intent parent replaced")
	}
	for _, p := range []string{IntentPath, canonicalRuntime + "/intent.json", StateDirectory + "/network-v1.json", canonicalRuntime + "/worker.sock", canonicalRuntime + "/worker.lock", StateDirectory + "/network.lock"} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return w, errors.New("security fixture requires reserved fresh state")
		}
	}
	return w, nil
}

func smokeACLFixture(owner Identity) (retErr error) {
	if _, err := smokeSecurityWitness(owner); err != nil {
		return err
	}
	dir := StateDirectory + "/.acl-probe"
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	file := dir + "/data"
	defer func() {
		_, err1 := smokeCommand("/bin/chmod", "-N", dir)
		_, err2 := smokeCommand("/bin/chmod", "-N", file)
		err3 := os.Remove(file)
		err4 := os.Remove(dir)
		retErr = errors.Join(retErr, err1, err2, err3, err4)
	}()
	f, err := rootFile(file, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY)
	if err != nil {
		return err
	}
	if err = f.Chmod(0755); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	validate := func(kind, path string) error {
		_, err := smokeCommand("/bin/sh", "-c", "set -eu\n"+installValidation+"\n"+kind+" \"$1\"", "bluegrass-security-fixture", path)
		return err
	}
	if checkRootDirectory(dir) != nil || validate("rootdir", dir) != nil || validate("rootfile", file) != nil {
		return errors.New("clean root-owned fixture rejected")
	}
	// chmod ACL principals are names/UUIDs, not numeric POSIX UIDs. The
	// well-known everyone principal exercises an unsafe grant on either runner
	// architecture without relying on directory-service name resolution.
	if _, err = smokeCommand("/bin/chmod", "+a", "everyone allow write", dir); err != nil {
		return err
	}
	var st unix.Stat_t
	if unix.Lstat(dir, &st) != nil || st.Uid != 0 || st.Mode&0777 != 0755 {
		return errors.New("ACL fixture must retain root:0755 metadata")
	}
	if checkRootDirectory(dir) == nil || validate("rootdir", dir) == nil {
		return errors.New("root-owned 0755 directory with writable ACL was accepted")
	}
	if opened, err := rootFile(file, unix.O_RDONLY); err == nil {
		_ = opened.Close()
		return errors.New("file under ACL-writable ancestor was accepted")
	}
	if _, err = smokeCommand("/bin/chmod", "-N", dir); err != nil {
		return err
	}
	if _, err = smokeCommand("/bin/chmod", "+a", "everyone allow write", file); err != nil {
		return err
	}
	if unix.Lstat(file, &st) != nil || st.Uid != 0 || st.Mode&0777 != 0755 {
		return errors.New("ACL file fixture must retain root:0755 metadata")
	}
	if opened, err := rootFile(file, unix.O_RDONLY); err == nil {
		_ = opened.Close()
		return errors.New("ACL-writable root file accepted")
	}
	if validate("rootfile", file) == nil {
		return errors.New("installer accepted ACL-writable root file")
	}
	return nil
}

func smokeDurableFixture(owner Identity) (retErr error) {
	w, err := smokeSecurityWitness(owner)
	if err != nil {
		return err
	}
	backup := canonicalRuntime + ".bootstrap-" + ExpectedWorkerSHA256
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return errors.New("runtime backup path already exists")
	}
	if err = os.Rename(canonicalRuntime, backup); err != nil {
		return err
	}
	var markerInfo os.FileInfo
	defer func() {
		if markerInfo != nil {
			current, err := os.Lstat(IntentPath)
			if err == nil && os.SameFile(markerInfo, current) {
				retErr = errors.Join(retErr, os.Remove(IntentPath))
			} else {
				retErr = errors.Join(retErr, errors.New("owned durable probe marker changed; retained"))
			}
		}
		actual, err := smokeDirectoryIdentity(backup)
		if err != nil || actual != w.Directories[canonicalRuntime] {
			retErr = errors.Join(retErr, errors.New("runtime probe directory replaced"))
			return
		}
		if _, err = os.Lstat(canonicalRuntime); !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, errors.New("runtime appeared; refusing overwrite during restore"))
			return
		}
		retErr = errors.Join(retErr, os.Rename(backup, canonicalRuntime))
	}()
	f, err := rootFile(IntentPath, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY)
	if err != nil {
		return err
	}
	markerInfo, err = f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	raw, _ := json.Marshal(Intent{Version: 1, OwnerUID: owner.UID, Active: true, RecoveryRequired: true})
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(raw)
	}
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if _, err = os.Lstat(canonicalRuntime); !errors.Is(err, os.ErrNotExist) {
		return errors.New("runtime must be absent during durable discovery")
	}
	i, err := ReadIntent()
	if err != nil || i == nil || !i.Active || i.OwnerUID != owner.UID {
		return errors.New("durable recovery intent lost when runtime is absent")
	}
	corrupt, err := rootFile(IntentPath, unix.O_WRONLY|unix.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = corrupt.Write([]byte("{"))
	err = errors.Join(err, corrupt.Close())
	if err != nil {
		return err
	}
	if _, err = ReadIntent(); !errors.Is(err, ErrRecovery) {
		return errors.New("corrupt durable marker permitted fallback with no runtime")
	}
	current, err := os.Lstat(IntentPath)
	if err != nil || !os.SameFile(markerInfo, current) {
		return errors.New("durable probe marker replaced")
	}
	if err = os.Remove(IntentPath); err != nil {
		return err
	}
	markerInfo = nil
	if absent, err := ReadIntent(); err != nil || absent != nil {
		return errors.New("absent durable and runtime markers did not permit fresh startup")
	}
	return nil
}

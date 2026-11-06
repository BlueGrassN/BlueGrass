//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type shellExecuteInfo struct {
	Size, Mask                        uint32
	Window                            windows.Handle
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance                          windows.Handle
	IDList                            uintptr
	Class                             *uint16
	ClassKey                          windows.Handle
	HotKey                            uint32
	Icon, Process                     windows.Handle
}

func requestElevation(ctx context.Context, exe, id, source string) error {
	if !hexID(id, 64) || !localAbsolute(source) {
		return errRequest
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--apply " + syscall.EscapeArg(id) + " " + syscall.EscapeArg(source))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file, Parameters: args, Directory: dir, Show: windows.SW_HIDE}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if callErr == windows.ERROR_CANCELLED {
			return errLaunchCancelled
		}
		return errLaunch
	}
	if info.Process == 0 {
		return errLaunch
	}
	defer windows.CloseHandle(info.Process)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		if ctx.Err() != nil {
			return errLaunch
		}
		status, err := windows.WaitForSingleObject(info.Process, 250)
		if err != nil {
			return errLaunch
		}
		if status == windows.WAIT_OBJECT_0 {
			var code uint32
			if windows.GetExitCodeProcess(info.Process, &code) != nil || code != 0 {
				return errLaunch
			}
			return nil
		}
		if status != uint32(windows.WAIT_TIMEOUT) {
			return errLaunch
		}
	}
}

// Apply is the elevated second stage. Source is an untrusted input directory,
// never an executable command. Only the signed manifest's exact derived package
// name is copied, then checked again in the protected machine staging directory.
// No key, feed, publisher, output location or installer arguments are accepted.
func Apply(ctx context.Context, id, source string) error {
	if !hexID(id, 64) || !localAbsolute(source) {
		return errRequest
	}
	cfg, err := compiledRelease()
	if err != nil {
		return err
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errTrust
	}
	i, err := trustedInstallation()
	if err != nil {
		return err
	}
	defer i.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	h, err := openPinned(source, true, windows.GENERIC_READ)
	if err != nil {
		return errPackage
	}
	defer windows.CloseHandle(h)
	b, err := readBounded(filepath.Join(source, "launch-"+id+".json"), update.MaxManifestBytes)
	if err != nil {
		return errPackage
	}
	var incoming state
	if strictObject(b, &incoming, "schema", "installation_id", "floor", "manifest") != nil || incoming.Schema != 1 || !hexID(incoming.InstallationID, 64) {
		return errState
	}
	dir, err := updateDirectory(true, cfg)
	if err != nil {
		return err
	}
	s, err := openDiskStore(dir, true)
	if err != nil {
		return err
	}
	defer s.Close()
	c, candidate, err := prepareApply(cfg, runtimeIdentity(i), s, incoming, id)
	if err != nil {
		return err
	}
	staged := filepath.Join(dir, id+".exe")
	if c.Revalidate(ctx, candidate, staged) != nil {
		if err := stagePackage(ctx, filepath.Join(source, id+".exe"), staged, candidate.Package().Size); err != nil {
			return errPackage
		}
	}
	// Pin before verification and retain through CreateProcess; ordinary users
	// cannot alter the machine-owned directory or the BA/SYSTEM-owned file.
	packageHandle, err := openPinned(staged, false, windows.GENERIC_READ|windows.READ_CONTROL)
	if err != nil {
		return errPackage
	}
	defer windows.CloseHandle(packageHandle)
	if validateACL(packageHandle, true) != nil || c.Revalidate(ctx, candidate, staged) != nil {
		return errPackage
	}
	if ctx.Err() != nil {
		return errLaunch
	}
	command := exec.Command(staged)
	command.Dir = dir
	if err := command.Start(); err != nil {
		return errLaunch
	}
	// Successful creation is not successful installation. Setup owns its own
	// explicit UI/maintenance workflow. Do not wait, kill or auto-resume a session.
	_ = command.Process.Release()
	return nil
}

func stagePackage(ctx context.Context, source, destination string, size int64) error {
	if size <= 0 || size > update.MaxPackageBytes {
		return errPackage
	}
	h, err := openPinned(source, false, windows.GENERIC_READ)
	if err != nil {
		return err
	}
	in := os.NewFile(uintptr(h), source)
	defer in.Close()
	info, err := in.Stat()
	if err != nil || info.Size() != size {
		return errPackage
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(destination), ".package-"+hex.EncodeToString(id[:])+".tmp")
	out, err := createExclusive(temp, true)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	buf := make([]byte, 64*1024)
	var copied int64
	for copied < size {
		if ctx.Err() != nil {
			out.Close()
			return ctx.Err()
		}
		n, err := in.Read(buf)
		if n > 0 {
			if copied+int64(n) > size {
				out.Close()
				return errPackage
			}
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			copied += int64(n)
		}
		if err != nil && err != io.EOF {
			out.Close()
			return err
		}
		if err == io.EOF {
			break
		}
	}
	syncErr := out.Sync()
	closeErr := out.Close()
	if copied != size || syncErr != nil || closeErr != nil {
		return errPackage
	}
	if h, err := openPinned(destination, false, windows.GENERIC_READ|windows.READ_CONTROL); err == nil {
		aclErr := validateACL(h, true)
		windows.CloseHandle(h)
		if aclErr != nil {
			return errPackage
		}
	} else if err != windows.ERROR_FILE_NOT_FOUND {
		return errPackage
	}
	from, _ := windows.UTF16PtrFromString(temp)
	to, _ := windows.UTF16PtrFromString(destination)
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

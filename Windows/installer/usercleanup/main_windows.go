//go:build windows

package main

import (
	"bluegrass/native/core/update"
	"bluegrass/native/core/winproxy"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--uninstall" {
		os.Exit(2)
	}
	if err := uninstallForCurrentUser(); err != nil {
		message("BlueGrass uninstall stopped. Close BlueGrass and run Uninstall BlueGrass from the original user's normal desktop. No other user's profile is cleaned.", 0x10)
		os.Exit(1)
	}
}

func message(text string, flags uintptr) uintptr {
	textPtr, _ := windows.UTF16PtrFromString(text)
	title, _ := windows.UTF16PtrFromString("BlueGrass")
	result, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(title)), flags)
	return result
}

func uninstallForCurrentUser() error {
	token := windows.GetCurrentProcessToken()
	if token.IsElevated() {
		return errors.New("original unelevated user required")
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	root := filepath.Join(programs, "BlueGrass")
	if !strings.EqualFold(exe, filepath.Join(root, "BlueGrass.UserCleanup.exe")) {
		return errors.New("invalid installed cleanup path")
	}
	publisher, err := update.TrustedPublisher(exe)
	if err != nil {
		return err
	}
	uninstaller := filepath.Join(root, "unins000.exe")
	if err := update.VerifyPublisher(uninstaller, publisher); err != nil {
		return err
	}
	if err := requireClosedProduct(token); err != nil {
		return err
	}
	if message("Uninstall BlueGrass for this computer? Current-user login tokens will be deleted and the owned proxy restored before administrator approval. Other users must clean their own profiles.", 0x134) != 6 {
		return nil
	}
	removeSettings := message("Also delete this user's saved BlueGrass preferences and device identity? Choosing No preserves them; login tokens are always deleted.", 0x124) == 6
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return err
	}
	owned := filepath.Join(local, "BlueGrass")
	for _, path := range []string{owned, filepath.Join(owned, "client"), filepath.Join(owned, "network")} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("owned directory is a reparse point")
		}
	}
	if err := cleanupRecords(filepath.Join(owned, "client"), removeSettings); err != nil {
		return err
	}
	network := filepath.Join(owned, "network")
	if _, err := os.Stat(network); err == nil {
		lock, err := os.OpenFile(filepath.Join(network, "proxy.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		var overlap windows.Overlapped
		if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap); err != nil {
			return err
		}
		defer windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &overlap)
		manager, err := winproxy.New(filepath.Join(network, "proxy-state.dpapi"))
		if err != nil {
			return err
		}
		if err := recoverProxy(manager); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(uninstaller)
	args, _ := windows.UTF16PtrFromString("/USERPHASE=complete")
	cwd, _ := windows.UTF16PtrFromString(root)
	return windows.ShellExecute(0, verb, file, args, cwd, windows.SW_SHOWNORMAL)
}

func requireClosedProduct(current windows.Token) error {
	user, err := current.GetTokenUser()
	if err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if !strings.EqualFold(name, "BlueGrass.exe") && !strings.EqualFold(name, "BlueGrass.Native.exe") {
			continue
		}
		process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			return openErr
		}
		var token windows.Token
		openErr = windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token)
		windows.CloseHandle(process)
		if openErr != nil {
			return openErr
		}
		peer, readErr := token.GetTokenUser()
		token.Close()
		if readErr != nil {
			return readErr
		}
		if peer.User.Sid.Equals(user.User.Sid) {
			return errors.New("close current-user GUI and native broker before uninstall")
		}
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return err
	}
	return nil
}

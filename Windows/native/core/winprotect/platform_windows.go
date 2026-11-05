//go:build windows && (amd64 || arm64)

package winprotect

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// New requires an elevated administrator or LocalSystem. It opens a single
// exclusive state writer and reconciles residue into blocking recovery. This
// constructor can mutate WFP on recovery; do not call it in unelevated tests.
func New(serviceExe, uiExe string) (*Manager, error) {
	if err := requireElevation(); err != nil {
		return nil, err
	}
	for _, path := range []string{serviceExe, uiExe} {
		if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.ContainsRune(path, 0) {
			return nil, errors.New("local absolute executable paths required")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("protection executable: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("protection executable is not a regular file")
		}
	}
	s, err := openStore()
	if err != nil {
		return nil, err
	}
	f, err := openFirewall()
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	m, err := newManager(serviceExe, uiExe, s, f, snapshotLinks, verifyProductTunnel)
	if err != nil {
		_ = f.Close()
		_ = s.Close()
		return nil, err
	}
	return m, nil
}

// Repair removes only this package's provider/sublayer/filter objects and
// protection intent. It requires elevation and an exclusive writer lock, so
// the service must be stopped first. TUN and proxy recovery belong to their
// respective owners and must be composed by the standalone recovery program.
func Repair() error {
	if err := requireElevation(); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	f, err := openFirewall()
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Remove(); err != nil {
		return err
	}
	return s.Save(intent{Version: 2})
}

func requireElevation() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	if u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) || token.IsElevated() {
		return nil
	}
	return errors.New("WFP protection requires an elevated administrator or LocalSystem")
}

func adapters() ([]byte, *windows.IpAdapterAddresses, error) {
	size := uint32(16 * 1024)
	for i := 0; i < 4; i++ {
		if size > 4*1024*1024 {
			return nil, nil, errors.New("adapter snapshot too large")
		}
		b := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&b[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, 0x10, nilUintptr, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if errors.Is(err, windows.ERROR_NO_DATA) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		return b, first, nil
	}
	return nil, nil, errors.New("adapter snapshot changed repeatedly")
}

const nilUintptr = uintptr(0)

func snapshotLinks() ([]link, error) {
	buf, first, err := adapters()
	if err != nil {
		return nil, err
	}
	defer runtime.KeepAlive(buf)
	var out []link
	for a := first; a != nil; a = a.Next {
		// Only up Ethernet/Wi-Fi links. Never grant LAN bypass through another
		// tunnel, software loopback, or the product's virtual interface.
		if a.OperStatus != windows.IfOperStatusUp || a.Luid == 0 || (a.IfType != 6 && a.IfType != 71) {
			continue
		}
		l := link{LUID: a.Luid}
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			ip, ok := netip.AddrFromSlice(u.Address.IP())
			if !ok {
				continue
			}
			ip = ip.Unmap()
			bits := int(u.OnLinkPrefixLength)
			if bits <= 0 || bits > ip.BitLen() || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			l.Prefixes = append(l.Prefixes, netip.PrefixFrom(ip, bits).Masked())
		}
		out = append(out, l)
	}
	return out, nil
}
func verifyProductTunnel(luid uint64) error {
	buf, first, err := adapters()
	if err != nil {
		return err
	}
	defer runtime.KeepAlive(buf)
	for a := first; a != nil; a = a.Next {
		if a.Luid == luid {
			name := windows.UTF16PtrToString(a.FriendlyName)
			if a.OperStatus != windows.IfOperStatusUp || a.IfType != 53 || !(strings.EqualFold(name, "BlueGrass") || strings.EqualFold(name, "BlueGrass-TUN")) {
				return errors.New("LUID is not an up BlueGrass Wintun interface")
			}
			return nil
		}
	}
	return errors.New("TUN LUID is not present")
}

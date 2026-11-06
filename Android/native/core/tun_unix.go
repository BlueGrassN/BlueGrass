//go:build android || linux

package bridge

import (
	tun "github.com/xtls/xray-core/proxy/tun"
	"golang.org/x/sys/unix"
	"os"
	"sync"
)

// The bridge supplies the FD directly to Xray's native stack. Using a small OS
// adapter avoids the process-global XRAY_TUN_FD handoff and its startup races.
// Kotlin keeps its original descriptor, including after a native stop/failure.
func openMobileTun(fd int) (tun.Tun, error) {
	duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, errStart
	}
	if unix.SetNonblock(duplicate, true) != nil {
		unix.Close(duplicate)
		return nil, errStart
	}
	file := os.NewFile(uintptr(duplicate), "BlueGrass-TUN")
	if file == nil {
		unix.Close(duplicate)
		return nil, errStart
	}
	return &mobileTun{file: file}, nil
}

type mobileTun struct {
	file    *os.File
	writeMu sync.Mutex
}

func (*mobileTun) Start() error                          { return nil }
func (t *mobileTun) Close() error                        { return t.file.Close() }
func (*mobileTun) Name() (string, error)                 { return "BlueGrass", nil }
func (*mobileTun) Index() (int, error)                   { return 0, nil }
func (t *mobileTun) ReadPacket(b []byte) ([]byte, error) { n, e := t.file.Read(b); return b[:n], e }
func (t *mobileTun) WritePacket(header, payload []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	packet := make([]byte, 0, len(header)+len(payload))
	packet = append(packet, header...)
	packet = append(packet, payload...)
	n, e := t.file.Write(packet)
	if e != nil {
		return e
	}
	if n != len(packet) {
		return errNetwork
	}
	return nil
}

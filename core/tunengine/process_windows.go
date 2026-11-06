//go:build windows

package tunengine

import (
	"encoding/binary"
	M "github.com/sagernet/sing/common/metadata"
	"golang.org/x/sys/windows"
	"net/netip"
	"unsafe"
)

var getExtendedTCPTable = ipHelper.NewProc("GetExtendedTcpTable")

// Match the complete original IPv4 TCP tuple. Failure gives no control bypass.
func connectionProcessPath(source, destination M.Socksaddr) string {
	if !source.Addr.Is4() || !destination.Addr.Is4() {
		return ""
	}
	size := uint32(0)
	status, _, _ := getExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 5, 0)
	if status != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) || size < 4 || size > 16<<20 {
		return ""
	}
	for attempts := 0; attempts < 3; attempts++ {
		data := make([]byte, size)
		status, _, _ = getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 5, 0)
		if status == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			if size > 16<<20 {
				return ""
			}
			continue
		}
		if status != 0 {
			return ""
		}
		count := binary.LittleEndian.Uint32(data[:4])
		if uint64(count)*24+4 > uint64(len(data)) {
			return ""
		}
		for i := uint32(0); i < count; i++ {
			row := data[4+i*24 : 4+(i+1)*24]
			local := netip.AddrFrom4([4]byte(row[4:8]))
			remote := netip.AddrFrom4([4]byte(row[12:16]))
			if local != source.Addr || remote != destination.Addr || binary.BigEndian.Uint16(row[8:10]) != source.Port || binary.BigEndian.Uint16(row[16:18]) != destination.Port {
				continue
			}
			handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, binary.LittleEndian.Uint32(row[20:24]))
			if e != nil {
				return ""
			}
			name := make([]uint16, 32768)
			length := uint32(len(name))
			e = windows.QueryFullProcessImageName(handle, 0, &name[0], &length)
			_ = windows.CloseHandle(handle)
			if e != nil {
				return ""
			}
			return windows.UTF16ToString(name[:length])
		}
		return ""
	}
	return ""
}

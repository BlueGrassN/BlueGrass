package mactun

import "encoding/binary"

// ATTR_CMN_EXTENDED_SECURITY contains an attrreference_t followed by either no
// data or Apple's exact KAUTH_FILESEC_NOACL. An empty/populated ACL is rejected.
func noACLBuffer(buffer []byte) bool {
	if len(buffer) < 12 {
		return false
	}
	total := binary.LittleEndian.Uint32(buffer[:4])
	offset := binary.LittleEndian.Uint32(buffer[4:8])
	size := binary.LittleEndian.Uint32(buffer[8:12])
	if total > uint32(len(buffer)) || offset != 8 {
		return false
	}
	if size == 0 {
		return total == 12
	}
	if size != 44 || total != 56 || binary.LittleEndian.Uint32(buffer[12:16]) != 0x012cc16d {
		return false
	}
	for _, b := range buffer[16:48] {
		if b != 0 {
			return false
		}
	}
	return binary.LittleEndian.Uint32(buffer[48:52]) == 0xffffffff && binary.LittleEndian.Uint32(buffer[52:56]) == 0
}

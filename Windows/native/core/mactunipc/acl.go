package mactunipc

import "encoding/binary"

// Darwin attrreference_t + kauth_filesec, from XNU sys/attr.h and sys/kauth.h.
// Accept only absent ACL or an unambiguous NOACL sentinel. Unknown layouts,
// flags, owner/group UUID security metadata and actual ACEs fail closed.
func noACLAttributes(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	total, offset, size := binary.LittleEndian.Uint32(b[:4]), binary.LittleEndian.Uint32(b[4:8]), binary.LittleEndian.Uint32(b[8:12])
	if total > uint32(len(b)) || offset != 8 {
		return false
	}
	if size == 0 {
		return total == 12
	}
	if size != 44 || total != 56 || binary.LittleEndian.Uint32(b[12:16]) != 0x012cc16d {
		return false
	}
	for _, v := range b[16:48] {
		if v != 0 {
			return false
		}
	}
	return binary.LittleEndian.Uint32(b[48:52]) == 0xffffffff && binary.LittleEndian.Uint32(b[52:56]) == 0
}

// A fixed ls -ldne producer emits the success record only on exit status zero.
// Check both suffix and extra ACL rows (xattr '@' can obscure the '+' suffix).
// Stream at most two 4096-byte lines; never capture an entire ACL listing.
const noACLListingFilter = `
length($0) > 4096 { bad=1; exit 1 }
NR == 1 {
    if (substr($1,1,1) !~ /^[d-]$/ || substr($1,2,9) !~ /^[rwxStTs-]+$/ ||
        (length($1) != 10 && !(length($1) == 11 && substr($1,11,1) == "@"))) { bad=1; exit 1 }
    next
}
NR == 2 && $0 == "BLUEGRASS_NO_ACL_CHECK_OK" { ok=1; next }
{ bad=1; exit 1 }
END { if (bad || !ok) exit 1 }
`

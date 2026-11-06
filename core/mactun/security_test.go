package mactun

import (
	"encoding/binary"
	"testing"
)

func TestNoACLProofRejectsTruncatedAndPopulatedSecurity(t *testing.T) {
	if noACLBuffer(nil) || noACLBuffer(make([]byte, 11)) {
		t.Fatal("accepted missing metadata")
	}
	plain := make([]byte, 12)
	binary.LittleEndian.PutUint32(plain[:4], 12)
	binary.LittleEndian.PutUint32(plain[4:8], 8)
	if !noACLBuffer(plain) {
		t.Fatal("no-security representation rejected")
	}
	full := make([]byte, 56)
	binary.LittleEndian.PutUint32(full[:4], 56)
	binary.LittleEndian.PutUint32(full[4:8], 8)
	binary.LittleEndian.PutUint32(full[8:12], 44)
	binary.LittleEndian.PutUint32(full[12:16], 0x012cc16d)
	binary.LittleEndian.PutUint32(full[48:52], 0xffffffff)
	if !noACLBuffer(full) {
		t.Fatal("NOACL representation rejected")
	}
	for _, offset := range []int{0, 4, 8, 12, 16, 48, 52} {
		copy := append([]byte(nil), full...)
		copy[offset] ^= 1
		if noACLBuffer(copy) {
			t.Fatalf("accepted mutated ACL at %d", offset)
		}
	}
}

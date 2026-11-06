package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

var testTransaction = [12]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}

// Hand-encoded XOR-MAPPED-ADDRESS: 192.0.2.1:32853, cookie 0x2112a442.
const validResponseHex = "0101000c2112a442000102030405060708090a0b002000080001a147e112a643"

func decodeHex(t testing.TB, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBindingRequest(t *testing.T) {
	want := decodeHex(t, "000100002112a442000102030405060708090a0b")
	if got := bindingRequest(testTransaction); !bytes.Equal(got, want) {
		t.Fatalf("binding request = %x, want %x", got, want)
	}
}

func TestParseBindingResponse(t *testing.T) {
	got, code := parseBindingResponse(decodeHex(t, validResponseHex), testTransaction)
	if code != "" || got.String() != "192.0.2.1:32853" {
		t.Fatalf("mapping = %v, code = %q", got, code)
	}
}

func TestRejectBindingResponse(t *testing.T) {
	for _, tc := range []struct{ name, packet, code string }{
		{"cookie", "0101000c2012a442000102030405060708090a0b002000080001a147e112a643", "stun_cookie"},
		{"transaction", "0101000c2112a442ff0102030405060708090a0b002000080001a147e112a643", "stun_transaction"},
		{"request_not_response", "0001000c2112a442000102030405060708090a0b002000080001a147e112a643", "stun_type"},
		{"error_response", "011100002112a442000102030405060708090a0b", "stun_response_error"},
		{"no_mapping", "010100002112a442000102030405060708090a0b", "stun_no_ipv4"},
		{"unaligned_length", "0101000b2112a442000102030405060708090a0b002000080001a147e112a643", "stun_malformed"},
		{"trailing_bytes", validResponseHex + "00", "stun_malformed"},
		{"attribute_overflow", "0101000c2112a442000102030405060708090a0b002000090001a147e112a643", "stun_malformed"},
		{"short_ipv4", "010100082112a442000102030405060708090a0b002000040001a147", "stun_malformed"},
		{"unknown_family", "0101000c2112a442000102030405060708090a0b002000080003a147e112a643", "stun_malformed"},
		{"short_ipv6", "0101000c2112a442000102030405060708090a0b002000080002a147e112a643", "stun_malformed"},
		{"ipv6_only", "010100182112a442000102030405060708090a0b002000140002a14700000000000000000000000000000000", "stun_no_ipv4"},
		{"bad_trailing_attribute", "010100102112a442000102030405060708090a0b002000080001a147e112a64380220005", "stun_malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, code := parseBindingResponse(decodeHex(t, tc.packet), testTransaction)
			if code != tc.code || got.IsValid() {
				t.Fatalf("mapping = %v, code = %q; want %q", got, code, tc.code)
			}
		})
	}
}

func TestEveryTruncatedBindingResponse(t *testing.T) {
	packet := decodeHex(t, validResponseHex)
	for n := 0; n < len(packet); n++ {
		got, code := parseBindingResponse(packet[:n], testTransaction)
		if code == "" || got.IsValid() {
			t.Fatalf("accepted truncated packet of %d bytes", n)
		}
	}
}

func TestAttributePaddingAndReservedByte(t *testing.T) {
	// SOFTWARE length 1 plus three padding bytes, followed by XOR-MAPPED-ADDRESS.
	packet := decodeHex(t, "010100142112a442000102030405060708090a0b8022000178ffffff00200008ff01a147e112a643")
	got, code := parseBindingResponse(packet, testTransaction)
	if code != "" || got.String() != "192.0.2.1:32853" {
		t.Fatalf("mapping = %v, code = %q", got, code)
	}
	// Even when its advertised payload fits, the last attribute needs its padding.
	packet = append(decodeHex(t, validResponseHex), 0x80, 0x22, 0, 1, 'x')
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)-20))
	if _, code = parseBindingResponse(packet, testTransaction); code != "stun_malformed" {
		t.Fatalf("accepted missing padding: %q", code)
	}
}

func FuzzParseBindingResponse(f *testing.F) {
	f.Add(decodeHex(f, validResponseHex))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, packet []byte) {
		mapping, code := parseBindingResponse(packet, testTransaction)
		if code == "" && (!mapping.IsValid() || !mapping.Addr().Is4()) {
			t.Fatalf("invalid successful mapping: %v", mapping)
		}
		if code != "" && mapping.IsValid() {
			t.Fatalf("failed parse leaked mapping: %v", mapping)
		}
	})
}

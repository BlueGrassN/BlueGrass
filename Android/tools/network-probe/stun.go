package main

import (
	"bytes"
	"encoding/binary"
	"net/netip"
)

const stunCookie uint32 = 0x2112a442

func bindingRequest(transaction [12]byte) []byte {
	packet := make([]byte, 20)
	binary.BigEndian.PutUint16(packet[:2], 0x0001)
	binary.BigEndian.PutUint32(packet[4:8], stunCookie)
	copy(packet[8:20], transaction[:])
	return packet
}

func parseBindingResponse(packet []byte, transaction [12]byte) (netip.AddrPort, string) {
	var empty netip.AddrPort
	if len(packet) < 20 {
		return empty, "stun_malformed"
	}
	length := int(binary.BigEndian.Uint16(packet[2:4]))
	if length%4 != 0 || len(packet) != 20+length {
		return empty, "stun_malformed"
	}
	if binary.BigEndian.Uint32(packet[4:8]) != stunCookie {
		return empty, "stun_cookie"
	}
	if !bytes.Equal(packet[8:20], transaction[:]) {
		return empty, "stun_transaction"
	}
	switch binary.BigEndian.Uint16(packet[:2]) {
	case 0x0101:
	case 0x0111:
		return empty, "stun_response_error"
	default:
		return empty, "stun_type"
	}
	var mapped netip.AddrPort
	for offset := 20; offset < len(packet); {
		if len(packet)-offset < 4 {
			return empty, "stun_malformed"
		}
		kind := binary.BigEndian.Uint16(packet[offset : offset+2])
		size := int(binary.BigEndian.Uint16(packet[offset+2 : offset+4]))
		offset += 4
		padded := (size + 3) &^ 3
		if padded > len(packet)-offset {
			return empty, "stun_malformed"
		}
		value := packet[offset : offset+size]
		if kind == 0x0020 {
			if size < 4 {
				return empty, "stun_malformed"
			}
			switch value[1] {
			case 1:
				if size != 8 {
					return empty, "stun_malformed"
				}
				if !mapped.IsValid() {
					var ip [4]byte
					binary.BigEndian.PutUint32(ip[:], binary.BigEndian.Uint32(value[4:8])^stunCookie)
					port := binary.BigEndian.Uint16(value[2:4]) ^ uint16(stunCookie>>16)
					mapped = netip.AddrPortFrom(netip.AddrFrom4(ip), port)
				}
			case 2:
				if size != 20 {
					return empty, "stun_malformed"
				}
			default:
				return empty, "stun_malformed"
			}
		}
		offset += padded
	}
	// Validate every attribute before returning, including those after the address.
	if !mapped.IsValid() {
		return empty, "stun_no_ipv4"
	}
	return mapped, ""
}

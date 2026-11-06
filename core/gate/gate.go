// Package gate implements deterministic eligibility, never OS protection.
package gate

import (
	"net/netip"
	"time"
)

func PublicIPv4(value string) bool {
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return false
	}
	b := ip.As4()
	return !(b[0] == 0 || b[0] == 100 && b[1] >= 64 && b[1] <= 127 || b[0] == 192 && b[1] == 0 && (b[2] == 0 || b[2] == 2) || b[0] == 198 && (b[1] == 18 || b[1] == 19 || b[1] == 51 && b[2] == 100) || b[0] == 203 && b[1] == 0 && b[2] == 113 || b[0] >= 224)
}
func Expired(now time.Time, until *time.Time) bool { return until != nil && !now.Before(*until) }

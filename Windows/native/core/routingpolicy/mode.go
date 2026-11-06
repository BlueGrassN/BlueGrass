package routingpolicy

import "errors"

// Mode selects the routing policy independently of the traffic takeover mode.
type Mode string

const (
	Global   Mode = "global"
	BypassCN Mode = "bypass_cn"
	// DomesticDNS is the IPv4 resolver for domains classified as Direct.
	DomesticDNS = "223.5.5.5"
)

// Normalize preserves the legacy global policy when the field is omitted.
// Only exact protocol values are accepted; whitespace and case are significant.
func Normalize(value string) (Mode, error) {
	switch Mode(value) {
	case "", Global:
		return Global, nil
	case BypassCN:
		return BypassCN, nil
	default:
		return "", errors.New("INVALID_ROUTING_MODE")
	}
}

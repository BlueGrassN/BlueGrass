//go:build !darwin

package tunengine

import (
	tun "github.com/xtls/xray-core/proxy/tun"
	"net/netip"
)

func nativeOptions(v4, v6 netip.Prefix) *tun.Config {
	// Gateway is assigned to the Windows host. DNS must target the unassigned
	// peer so the on-link route sends queries into Wintun, not local delivery.
	return &tun.Config{Name: InterfaceName, Desc: "Wintun", Gateway: []string{v4.String(), v6.String()}, MTU: 1500,
		DNS: []string{v4.Addr().Next().String()}, AutoSystemRoutingTable: []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}}
}

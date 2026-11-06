//go:build darwin

package tunengine

import (
	tun "github.com/xtls/xray-core/proxy/tun"
	"net/netip"
)

func nativeOptions(v4, v6 netip.Prefix) *tun.Config {
	// Xray Darwin assigns v4.Next() to the host and v4 to the remote peer.
	// DNS is retained here as stack/host-manager intent, but removed from the
	// cloned config passed to the native opener. No automatic host mutations.
	return &tun.Config{Name: InterfaceName, Desc: "BlueGrass utun", MTU: 1500,
		Gateway: []string{v4.String(), v6.String()}, DNS: []string{v4.Addr().String()}}
}

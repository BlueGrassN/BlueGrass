//go:build !darwin

package tunengine

import (
	"encoding/json"
	"net/netip"
	"testing"
)

func TestNativeTUNUsesXrayOwnedRoutesAndDNS(t *testing.T) {
	options := nativeOptions(netip.MustParsePrefix("172.31.255.1/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126"))
	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Name          string   `json:"name"`
		Gateway       []string `json:"gateway"`
		DNS           []string `json:"DNS"`
		Routes        []string `json:"auto_system_routing_table"`
		AutoInterface string   `json:"auto_outbounds_interface"`
		Firewall      []string `json:"auto_system_wfp_block_leak"`
	}
	if err := json.Unmarshal(encoded, &config); err != nil {
		t.Fatal(err)
	}
	if config.Name != InterfaceName || len(config.Gateway) != 2 || len(config.DNS) != 1 || config.DNS[0] != "172.31.255.2" || len(config.Routes) != 4 {
		t.Fatal("native TUN must use Xray's gateway, DNS and dual-stack split-default route configuration")
	}
	if config.AutoInterface != "" || len(config.Firewall) != 0 {
		t.Fatal("host owns physical binding and WFP")
	}
}

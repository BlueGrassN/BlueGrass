package control

import (
	"encoding/json"
	"testing"
)

func TestRoutingModeIndependentFromIngress(t *testing.T) {
	for _, ingress := range []string{"system_proxy", "tun"} {
		for _, mode := range []string{"", "global", "bypass_cn"} {
			r := request(Connect, "routing", 1)
			r.Params.Profile.Mode = ingress
			r.Params.Profile.RoutingMode = mode
			wire, _ := json.Marshal(r)
			decoded, err := DecodeRequest(wire)
			if err != nil || decoded.Params.Profile.RoutingMode != mode || ValidateProfile(*decoded.Params.Profile, baseTime) != "" {
				t.Fatalf("lost mode %q/%q: %v", ingress, mode, err)
			}
		}
	}
	p := profile()
	p.RoutingMode = "direct_all"
	if ValidateProfile(p, baseTime) != "INVALID_MODE" {
		t.Fatal("unknown routing mode admitted")
	}
}

func TestRoutingModeWireRejectsInvalidExplicitValues(t *testing.T) {
	for _, value := range []any{"", "auto", nil, true, 42} {
		wire, _ := json.Marshal(request(Connect, "routing", 1))
		var raw map[string]any
		_ = json.Unmarshal(wire, &raw)
		raw["params"].(map[string]any)["profile"].(map[string]any)["routingMode"] = value
		wire, _ = json.Marshal(raw)
		if _, err := DecodeRequest(wire); err == nil {
			t.Fatalf("accepted invalid explicit routing value %v", value)
		}
	}
}

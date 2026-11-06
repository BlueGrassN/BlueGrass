package bridge

import (
	"bluegrass/native/core/protocolconfig"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateResourceWithoutExpectedExit(t *testing.T) {
	if err := Validate(validConfig); err != nil {
		t.Fatal(err)
	}
}

func TestResourceEndpointAndCredentialsSurviveNativeOptionSerialization(t *testing.T) {
	c, err := parseConfig(validConfig)
	if err != nil {
		t.Fatal(err)
	}
	options, err := protocolconfig.Build(c.Protocol, netip.MustParseAddrPort("8.8.4.4:1080"))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(options)
	if err != nil || !strings.Contains(string(wire), "8.8.4.4") || !strings.Contains(string(wire), "fixture") || !strings.Contains(string(wire), "FAKE_SECRET") {
		t.Fatal("validated endpoint or credentials lost")
	}

}
func TestValidateRejectsMalformedAndExpiredWithoutLeakingInput(t *testing.T) {
	for name, raw := range map[string]string{
		"expired":       strings.Replace(validConfig, `"expiresAtUtc":null`, `"expiresAtUtc":"2026-10-13T08:00:00Z"`, 1),
		"duplicate":     strings.Replace(validConfig, `"port":1080`, `"port":1080,"port":80`, 1),
		"unknown":       strings.Replace(validConfig, `"port":1080`, `"port":1080,"outbound":"direct"`, 1),
		"not-utc":       strings.Replace(validConfig, `2026-10-13T08:00:00Z`, `2026-10-13T08:00:00`, 1),
		"private-entry": strings.Replace(validConfig, `entry.example.invalid`, `192.168.1.1`, 1),
		"url-entry":     strings.Replace(validConfig, `entry.example.invalid`, `https://secret@example.invalid`, 1),
		"ipv6-entry":    strings.Replace(validConfig, `entry.example.invalid`, `2001:4860:4860::8888`, 1),
		"null-protocol": strings.Replace(validConfig, `{"version":1,"type":"socks5","socks5":{"username":"fixture","password":"FAKE_SECRET"}}`, `null`, 1),
		"oversize":      validConfig + strings.Repeat(" ", 32769),
	} {
		t.Run(name, func(t *testing.T) {
			err := Validate(raw)
			if err == nil {
				t.Fatal("accepted invalid config")
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "example") {
				t.Fatal("error retained input")
			}
		})
	}
}
func TestLANUsesKnownNetworkAndNeverDefaultRoute(t *testing.T) {
	info := fixtureNetwork
	info.Routes = []string{"0.0.0.0/0", "::/0", "10.10.0.0/16", "8.8.8.0/24"}
	ranges := localRanges(info)
	for _, tt := range []struct {
		ip   string
		want bool
	}{
		{"192.168.8.100", true}, {"10.10.2.1", true}, {"8.8.8.8", false}, {"1.1.1.1", false}, {"2001:4860:4860::8888", false}, {"172.19.0.2", false},
	} {
		got := false
		for _, r := range ranges {
			if r.Contains(netip.MustParseAddr(tt.ip)) {
				got = true
			}
		}
		if got != tt.want {
			t.Errorf("%s: got %v", tt.ip, got)
		}
	}
}
func TestGeneratedOptionsAcceptedByPinnedXray(t *testing.T) {
	c, err := parseConfig(validConfig)
	if err != nil {
		t.Fatal(err)
	}
	options, err := protocolconfig.Build(c.Protocol, netip.MustParseAddrPort("8.8.4.4:1080"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = options.Build(); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedProtocolsFailBeforeNetworkAndNeverFallBack(t *testing.T) {
	for _, kind := range []string{"tuic", "anytls"} {
		c, err := parseConfig(validConfig)
		if err != nil {
			t.Fatal(err)
		}
		c.Protocol = protocolconfig.Config{Version: 1, Type: kind, TLS: &protocolconfig.TLS{ServerName: "entry.example.invalid"}}
		if kind == "tuic" {
			c.Protocol.TUIC = &protocolconfig.TUIC{UUID: "11111111-1111-4111-8111-111111111111", Password: "fixture", CongestionControl: "cubic", UDPRelayMode: "native"}
		} else {
			c.Protocol.AnyTLS = &protocolconfig.AnyTLS{Password: "fixture"}
		}
		if err = c.Protocol.Validate(); err != nil {
			t.Fatal("wire schema no longer retained", err)
		}
		wire, _ := json.Marshal(c)
		if err = Validate(string(wire)); !errors.Is(err, protocolconfig.ErrUnsupportedProtocol) {
			t.Fatal("missing explicit unsupported diagnostic", err)
		}
		platform := &fakePlatform{}
		if session, err := Start(string(wire), 42, platform); session != nil || !errors.Is(err, protocolconfig.ErrUnsupportedProtocol) {
			t.Fatal("unsupported core tried a fallback", err)
		}
	}
}

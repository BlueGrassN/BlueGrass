package protocolconfig

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

const fixtureUUID = "7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81"

func fixtures() []Config {
	tls := &TLS{ServerName: "entry.example"}
	return []Config{
		{Version: 1, Type: "socks5", SOCKS5: &SOCKS5{Username: "fixture-user", Password: "fixture-password"}},
		{Version: 1, Type: "shadowsocks", Shadowsocks: &Shadowsocks{Method: "aes-128-gcm", Password: "fixture-password"}},
		{Version: 1, Type: "vmess", VMess: &VMess{UUID: fixtureUUID, Security: "aes-128-gcm", Transport: "tcp"}},
		{Version: 1, Type: "vless", TLS: tls, VLESS: &VLESS{UUID: fixtureUUID, Transport: "tcp", Flow: "xtls-rprx-vision"}},
		{Version: 1, Type: "trojan", TLS: tls, Trojan: &Trojan{Password: "fixture-password", Transport: "tcp"}},
		{Version: 1, Type: "http", HTTP: &HTTP{}},
		{Version: 1, Type: "https", TLS: tls, HTTP: &HTTP{Username: "fixture-user", Password: "fixture-password"}},
		{Version: 1, Type: "hysteria2", TLS: tls, Hysteria2: &Hysteria2{Password: "fixture-password", Obfs: &Obfs{Type: "salamander", Password: "fixture-obfs"}}},
		{Version: 1, Type: "tuic", TLS: tls, TUIC: &TUIC{UUID: fixtureUUID, Password: "fixture-password", CongestionControl: "bbr", UDPRelayMode: "native"}},
		{Version: 1, Type: "anytls", TLS: tls, AnyTLS: &AnyTLS{Password: "fixture-password"}},
	}
}

func TestBuildTypedOptionsKeepsLiteralEntryAndExplicitTLSIdentity(t *testing.T) {
	entry := netip.MustParseAddrPort("8.8.8.8:8443")
	for _, c := range fixtures() {
		t.Run(c.Type, func(t *testing.T) {
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var parsed Config
			if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Type != c.Type {
				t.Fatal("wire schema round trip failed", err)
			}
			o, err := Build(c, entry)
			if c.Type == "tuic" || c.Type == "anytls" {
				if err != ErrUnsupportedProtocol || o != nil {
					t.Fatal("unsupported core protocol was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			if json.Unmarshal(*o.Settings, &settings) != nil || settings["address"] != "8.8.8.8" || settings["port"] != float64(8443) || o.Tag != "selected" {
				t.Fatal("resource endpoint changed")
			}
			if o.SendThrough != nil || o.ProxySettings != nil || o.MuxSettings != nil || o.StreamSetting.SocketSettings != nil {
				t.Fatal("arbitrary dialer escape")
			}
			if c.TLS != nil {
				tls := o.StreamSetting.TLSSettings
				if o.StreamSetting.Security != "tls" || tls.AllowInsecure || tls.DisableSystemRoot || tls.ServerName != "entry.example" || len(tls.Certs) != 0 || tls.Fingerprint != "" || tls.MinVersion < "1.2" {
					t.Fatal("TLS identity or trust weakened")
				}
			}
			// Compile the actual Xray protobuf; shape-only tests miss rejected options.
			if _, err := o.Build(); err != nil {
				t.Fatal("Xray rejected validated resource config", err)
			}
		})
	}
}

func TestConfigRejectsUntypedAmbiguousAndUnsafeParameters(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"version":2,"type":"http","http":{}}`,
		`{"version":1,"type":"http","http":{},"trojan":{"password":"secret"}}`,
		`{"version":1,"type":"http","http":{},"type":"https"}`,
		`{"version":1,"Type":"http","http":{}}`,
		`{"version":1,"type":"http","http":{"headers":{"X-Test":"secret"}}}`,
		`{"version":1,"type":"http","http":{},"outbounds":[]}`,
		`{"version":1,"type":"https","http":{},"tls":{"serverName":"entry.example","insecure":true}}`,
		`{"version":1,"type":"https","http":{},"tls":{"serverName":"entry.example","certificatePath":"C:\\secret"}}`,
		`{"version":1,"type":"https","http":{},"tls":{"serverName":"entry.example","serverName":"other.example"}}`,
		`{"version":1,"type":"vmess","vmess":{"uuid":"not-a-uuid","security":"none","transport":"ws"}}`,
		`{"version":1,"type":"shadowsocks","shadowsocks":{"method":"none","password":"secret"}}`,
		`{"version":1,"type":"http","http":{}} {}`,
	} {
		var c Config
		if err := json.Unmarshal([]byte(raw), &c); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe config accepted or raw input exposed")
		}
	}
	for _, c := range fixtures() {
		if c.TLS != nil {
			c.TLS = nil
			if _, err := Build(c, netip.MustParseAddrPort("8.8.8.8:443")); err == nil {
				t.Fatal("required TLS silently disabled", c.Type)
			}
		}
	}
	for _, entry := range []string{"127.0.0.1:443", "192.168.1.1:443", "198.18.0.2:443", "[2606:4700::1111]:443", "8.8.8.8:0"} {
		if _, err := Build(fixtures()[0], netip.MustParseAddrPort(entry)); err == nil {
			t.Fatal("invalid entry accepted")
		}
	}
}

func TestSS2022KeysAndTLSParametersAreCheckedBeforeNativeConstruction(t *testing.T) {
	c := Config{Version: 1, Type: "shadowsocks", Shadowsocks: &Shadowsocks{Method: "2022-blake3-aes-128-gcm", Password: base64.StdEncoding.EncodeToString(make([]byte, 16))}}
	if c.Validate() != nil {
		t.Fatal("correct SS2022 key rejected")
	}
	for _, key := range []string{"short", base64.StdEncoding.EncodeToString(make([]byte, 32)), c.Shadowsocks.Password + ":" + c.Shadowsocks.Password} {
		c.Shadowsocks.Password = key
		if c.Validate() == nil {
			t.Fatal("invalid/implicit chained key accepted")
		}
	}
	for _, tls := range []*TLS{{}, {ServerName: "*.example"}, {ServerName: "https://example.com"}, {ServerName: "example.com\r\n"},
		{ServerName: "entry.example", ALPN: []string{"h2"}}, {ServerName: "entry.example", ALPN: []string{"http/1.1", "http/1.1"}}} {
		c = Config{Version: 1, Type: "https", HTTP: &HTTP{}, TLS: tls}
		if c.Validate() == nil {
			t.Fatal("unsafe HTTPS/TLS combination accepted")
		}
	}
}

func TestBuildDetachesTLSAndNeverMapsLegacyCredentials(t *testing.T) {
	c := Config{Version: 1, Type: "https", HTTP: &HTTP{}, TLS: &TLS{ServerName: "entry.example", ALPN: []string{"http/1.1"}}}
	o, err := Build(c, netip.MustParseAddrPort("8.8.8.8:443"))
	if err != nil {
		t.Fatal(err)
	}
	c.TLS.ServerName, c.TLS.ALPN[0] = "evil.example", "h2"
	tls := o.StreamSetting.TLSSettings
	if tls.ServerName != "entry.example" || (*tls.ALPN)[0] != "http/1.1" {
		t.Fatal("caller mutated prepared TLS")
	}
	if (Config{Version: 1, Type: "vless", SOCKS5: &SOCKS5{Username: fixtureUUID, Password: "secret"}}).Validate() == nil {
		t.Fatal("SOCKS fields invented VLESS settings")
	}
}

func TestRequirementsOnlyRequestExactPhysicalEntryTransports(t *testing.T) {
	want := map[string]TransportRequirements{
		"socks5":      {EntryTCP: true, UDPTransport: "authenticated-socks-relay"},
		"shadowsocks": {EntryTCP: true, EntryUDP: true, UDPTransport: "entry-datagram"},
		"vmess":       {EntryTCP: true, UDPTransport: "over-tcp"},
		"vless":       {EntryTCP: true, UDPTransport: "over-tcp"},
		"trojan":      {EntryTCP: true, UDPTransport: "over-tcp"},
		"http":        {EntryTCP: true, UDPTransport: "none"},
		"https":       {EntryTCP: true, UDPTransport: "none"},
		"hysteria2":   {EntryUDP: true, UDPTransport: "quic"},
	}
	for _, c := range fixtures() {
		got, err := Requirements(c)
		if c.Type == "tuic" || c.Type == "anytls" {
			if err != ErrUnsupportedProtocol || got.EntryTCP || got.EntryUDP {
				t.Fatal("unsupported protocol granted physical transport")
			}
			continue
		}
		if err != nil || got != want[c.Type] {
			t.Fatal("overbroad or missing physical entry permission", c.Type)
		}
	}
	if r, err := Requirements(Config{}); err == nil || r.EntryTCP || r.EntryUDP {
		t.Fatal("invalid protocol granted physical transport")
	}
}

func TestHysteriaPreservesAuthenticationAndSalamander(t *testing.T) {
	c := fixtures()[7]
	o, err := Build(c, netip.MustParseAddrPort("8.8.8.8:443"))
	if err != nil {
		t.Fatal(err)
	}
	s := o.StreamSetting
	if o.Protocol != "hysteria" || *s.Network != "hysteria" || s.HysteriaSettings.Auth != c.Hysteria2.Password || s.TLSSettings.MinVersion != "1.3" || (*s.TLSSettings.ALPN)[0] != "h3" {
		t.Fatal("Hysteria identity/authentication lost")
	}
	if s.FinalMask == nil || len(s.FinalMask.Udp) != 1 || s.FinalMask.Udp[0].Type != "salamander" {
		t.Fatal("obfuscation lost")
	}
	var obfs map[string]any
	if json.Unmarshal(*s.FinalMask.Udp[0].Settings, &obfs) != nil || obfs["password"] != c.Hysteria2.Obfs.Password {
		t.Fatal("obfuscation authentication lost")
	}
	if _, err := o.Build(); err != nil {
		t.Fatal(err)
	}
}

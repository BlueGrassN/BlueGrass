package nativehost

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/engine"
)

type profileBoundaryEngine struct {
	tunnelStub
	lanCalls int
	prepared chan engine.Profile
}

func (e *profileBoundaryEngine) SetAllowLAN(bool) error { e.lanCalls++; return nil }
func (e *profileBoundaryEngine) Prepare(_ context.Context, p engine.Profile) error {
	e.prepared <- p
	return nil
}

func profileWire(t *testing.T, protocol, config string, auth engine.Auth) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(reviewProfile("protocol-resource"), &fields); err != nil {
		t.Fatal(err)
	}
	fields["mode"] = json.RawMessage(`"tun"`)
	fields["protocol"], _ = json.Marshal(protocol)
	fields["auth"], _ = json.Marshal(auth)
	if config != "" {
		fields["protocolConfig"] = json.RawMessage(config)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProfileTypedConfigReachesEngineWithoutLegacyInference(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, config string
		auth                   engine.Auth
	}{
		{"legacy-socks", "socks5", "", engine.Auth{Username: "fixture-user", Password: "fixture-password"}},
		{"typed-socks", "socks5", `{"version":1,"type":"socks5","socks5":{"username":"fixture-user","password":"fixture-password"}}`, engine.Auth{}},
		{"matching-socks-auth", "socks5", `{"version":1,"type":"socks5","socks5":{"username":"fixture-user","password":"fixture-password"}}`, engine.Auth{Username: "fixture-user", Password: "fixture-password"}},
		{"shadowsocks", "shadowsocks", `{"version":1,"type":"shadowsocks","shadowsocks":{"method":"aes-128-gcm","password":"fixture-password"}}`, engine.Auth{}},
		{"vmess", "vmess", `{"version":1,"type":"vmess","vmess":{"uuid":"7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81","security":"aes-128-gcm","transport":"tcp"}}`, engine.Auth{}},
		{"vless", "vless", `{"version":1,"type":"vless","tls":{"serverName":"entry.example"},"vless":{"uuid":"7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81","transport":"tcp"}}`, engine.Auth{}},
		{"http", "http", `{"version":1,"type":"http","http":{}}`, engine.Auth{}},
		{"https", "https", `{"version":1,"type":"https","tls":{"serverName":"entry.example"},"http":{"username":"fixture-user","password":"` + strings.Repeat("p", 1024) + `"}}`, engine.Auth{}},
		{"trojan", "trojan", `{"version":1,"type":"trojan","tls":{"serverName":"entry.example"},"trojan":{"password":"fixture-password","transport":"tcp"}}`, engine.Auth{}},
		{"hysteria2", "hysteria2", `{"version":1,"type":"hysteria2","tls":{"serverName":"entry.example"},"hysteria2":{"password":"fixture-password"}}`, engine.Auth{}},
		{"tuic", "tuic", `{"version":1,"type":"tuic","tls":{"serverName":"entry.example"},"tuic":{"uuid":"7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81","password":"fixture-password","congestionControl":"bbr","udpRelayMode":"native"}}`, engine.Auth{}},
		{"anytls", "anytls", `{"version":1,"type":"anytls","tls":{"serverName":"entry.example"},"anytls":{"password":"fixture-password"}}`, engine.Auth{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &profileBoundaryEngine{prepared: make(chan engine.Profile, 1)}
			h := dnsHost(t, e, "tun")
			p := profileWire(t, tc.protocol, tc.config, tc.auth)
			if r := h.Handle(Request{ID: "typed-connect", Command: "Connect", Profile: p}); !r.OK {
				t.Fatalf("bounded typed profile rejected: %s", r.Error)
			}
			select {
			case got := <-e.prepared:
				wire, _ := json.Marshal(got)
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(wire, &fields)
				if got.Protocol != tc.protocol || got.Auth != tc.auth || (tc.config != "" && len(fields["protocolConfig"]) == 0) {
					t.Fatal("accepted typed configuration was dropped or mapped through legacy auth")
				}
				if tc.config != "" {
					var expected, actual any
					_ = json.Unmarshal([]byte(tc.config), &expected)
					_ = json.Unmarshal(fields["protocolConfig"], &actual)
					if !reflect.DeepEqual(expected, actual) {
						t.Fatal("typed configuration changed before reaching the engine")
					}
				}
			case <-time.After(time.Second):
				t.Fatal("accepted profile did not reach the engine")
			}
		})
	}
}

func TestProfileResourceUpdateRejectsUnboundedProfileBeforeClockChange(t *testing.T) {
	h, _ := trafficSamplingHost(t)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(profileWire(t, "socks5", "", engine.Auth{}), &fields)
	var key map[string]string
	_ = json.Unmarshal(fields["key"], &key)
	// Keep the current resource identity, but send an unbounded legacy password.
	key["sourceId"] = h.context.ResourceKey.SourceID
	fields["key"], _ = json.Marshal(key)
	fields["auth"], _ = json.Marshal(engine.Auth{Username: "fixture-user", Password: strings.Repeat("p", 256)})
	p, _ := json.Marshal(fields)
	update, _ := json.Marshal(map[string]any{"revision": 2, "validUntilUtc": nil, "unavailable": false, "profile": json.RawMessage(p)})
	future := h.clock.Read().EffectiveUTC.Add(time.Hour)
	r := h.Handle(Request{ID: "invalid-update-profile", Command: "ApplyResourceUpdate", Update: update, ServerTimeUTC: &future})
	if r.OK || r.Error != "INVALID_CONFIGURATION" || !h.clock.Read().EffectiveUTC.Before(future) || r.Snapshot["businessGateOpen"] != true {
		t.Fatal("invalid resource profile changed clock or active forwarding")
	}
}

func TestProfileInvalidConfigRejectedBeforeClockOrEngineChanges(t *testing.T) {
	base := profileWire(t, "socks5", "", engine.Auth{})
	for _, tc := range []struct {
		name    string
		profile json.RawMessage
	}{
		{"unknown-profile-field", append(append(json.RawMessage{}, base[:len(base)-1]...), []byte(`,"dialer":{"detour":"direct"}}`)...)},
		{"unknown-auth-field", json.RawMessage(strings.Replace(string(base), `"username":""`, `"username":"","extra":true`, 1))},
		{"case-variant", json.RawMessage(strings.Replace(string(base), `"protocol":`, `"Protocol":`, 1))},
		{"null-entry", json.RawMessage(strings.Replace(string(base), `{"host":"1.1.1.1","port":443}`, `null`, 1))},
		{"null-config", profileWire(t, "socks5", `null`, engine.Auth{})},
		{"unknown-config-field", profileWire(t, "socks5", `{"version":1,"type":"socks5","socks5":{},"outbounds":[]}`, engine.Auth{})},
		{"config-type-conflict", profileWire(t, "socks5", `{"version":1,"type":"http","http":{}}`, engine.Auth{})},
		{"legacy-auth-conflict", profileWire(t, "socks5", `{"version":1,"type":"socks5","socks5":{"username":"a","password":"b"}}`, engine.Auth{Username: "a", Password: "different"})},
		{"legacy-nonsocks-inference", profileWire(t, "trojan", "", engine.Auth{Password: "fixture-password"})},
		{"legacy-auth-with-trojan", profileWire(t, "trojan", `{"version":1,"type":"trojan","tls":{"serverName":"entry.example"},"trojan":{"password":"fixture-password","transport":"tcp"}}`, engine.Auth{Password: "fixture-password"})},
		{"overlong-config-credential", profileWire(t, "http", `{"version":1,"type":"http","http":{"username":"a","password":"`+strings.Repeat("p", 1025)+`"}}`, engine.Auth{})},
		{"oversized-config", profileWire(t, "socks5", `{"version":1,"type":"socks5","socks5":{"username":"`+strings.Repeat("a", 17000)+`","password":"b"}}`, engine.Auth{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &profileBoundaryEngine{prepared: make(chan engine.Profile, 1)}
			h := dnsHost(t, e, "tun")
			before := h.clock.Read().EffectiveUTC
			future := before.Add(time.Hour)
			r := h.Handle(Request{ID: "invalid-config", Command: "Connect", Profile: tc.profile, ServerTimeUTC: &future})
			if r.OK || r.Error != "INVALID_CONFIGURATION" || e.lanCalls != 0 || h.controller != nil || !h.clock.Read().EffectiveUTC.Before(future) {
				t.Fatalf("invalid configuration affected state: ok=%v error=%s lanCalls=%d", r.OK, r.Error, e.lanCalls)
			}
		})
	}
}

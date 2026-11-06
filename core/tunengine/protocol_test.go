package tunengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/protocolconfig"
)

func TestAllTypedProtocolsConstructFullRouterWithoutNativeStart(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"type":"socks5","socks5":{}}`,
		`{"version":1,"type":"shadowsocks","shadowsocks":{"method":"aes-256-gcm","password":"fixture-password"}}`,
		`{"version":1,"type":"vmess","vmess":{"uuid":"7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81","security":"aes-128-gcm","transport":"tcp"}}`,
		`{"version":1,"type":"vless","vless":{"uuid":"7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81","transport":"tcp","flow":"xtls-rprx-vision"},"tls":{"serverName":"entry.example"}}`,
		`{"version":1,"type":"trojan","trojan":{"password":"fixture-password","transport":"tcp"},"tls":{"serverName":"entry.example"}}`,
		`{"version":1,"type":"http","http":{}}`,
		`{"version":1,"type":"https","http":{},"tls":{"serverName":"entry.example"}}`,
		`{"version":1,"type":"hysteria2","hysteria2":{"password":"fixture-password"},"tls":{"serverName":"entry.example"}}`,
		`{"version":1,"type":"tuic","tuic":{"uuid":"7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81","password":"fixture-password","congestionControl":"bbr","udpRelayMode":"native"},"tls":{"serverName":"entry.example"}}`,
		`{"version":1,"type":"anytls","anytls":{"password":"fixture-password"},"tls":{"serverName":"entry.example"}}`,
	} {
		var pc protocolconfig.Config
		if json.Unmarshal([]byte(raw), &pc) != nil {
			t.Fatal("fixture invalid")
		}
		t.Run(pc.Type, func(t *testing.T) {
			c := testConfig()
			n, _ := c.Network(context.Background())
			p := testProfile()
			p.Protocol = pc.Type
			prepared, err := prepareProtocolConfig(p, c, n, netip.MustParseAddr("8.8.8.8"), pc)
			if pc.Type == "anytls" || pc.Type == "tuic" {
				if !errors.Is(err, protocolconfig.ErrUnsupportedProtocol) {
					t.Fatal("unsupported Xray protocol did not return its explicit error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			g := newSessionGate()
			r, err := newRuntime(context.Background(), prepared, g)
			if err != nil {
				t.Fatal(err)
			}
			if r.InterfaceLUID() != 0 || g.isOpen() {
				t.Fatal("constructor started interface/gate")
			}
			if err = r.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTypedProtocolUsesRealXrayWhileGateAndNativeInterfaceStayClosed(t *testing.T) {
	pc := protocolconfig.Config{Version: 1, Type: "https", HTTP: &protocolconfig.HTTP{}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}
	c := testConfig()
	n, _ := c.Network(context.Background())
	p := testProfile()
	p.Protocol = "https"
	p.Auth.Username, p.Auth.Password = "ignored-legacy", "ignored-legacy"
	prepared, err := prepareProtocolConfig(p, c, n, netip.MustParseAddr("8.8.8.8"), pc)
	if err != nil {
		t.Fatal(err)
	}
	g := newSessionGate()
	r, err := newRuntime(context.Background(), prepared, g)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out, ok := r.(*xrayRuntime).selected, r.(*xrayRuntime).selected != nil
	if !ok || out.config.Protocol != "http" || r.InterfaceLUID() != 0 || g.isOpen() {
		t.Fatal("typed selection escaped preparation gate")
	}
}

func TestTypedPrepareCannotInferConfigOrReuseProof(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	p := testProfile()
	if e.Prepare(context.Background(), p) != nil {
		t.Fatal("legacy prepare failed")
	}
	if _, err := e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetBusinessGate(true) != nil {
		t.Fatal("proof fixture failed")
	}
	p.Protocol = "https"
	if e.Prepare(context.Background(), p) == nil || e.VerifiedIPv4() != "" {
		t.Fatal("legacy auth inferred HTTPS or retained proof")
	}
	pc := protocolconfig.Config{Version: 1, Type: "https", HTTP: &protocolconfig.HTTP{}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}
	if err := e.PrepareProtocol(context.Background(), p, pc); err != nil {
		t.Fatal(err)
	}
	if e.gate.isOpen() || e.VerifiedIPv4() != "" || e.SetBusinessGate(true) != nil {
		t.Fatal("protocol switch retained observation or cannot open new session")
	}
	pc.Type = "http"
	err := e.PrepareProtocol(context.Background(), p, pc)
	e.mu.Lock()
	runtime := e.runtime
	e.mu.Unlock()
	if err == nil || runtime != nil {
		t.Fatal("mismatched protocol accepted")
	}
}

func TestTunBothPreparePathsRejectConflictingAuthAndRetireProof(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
		e := newTestEngine(t, r)
		p := testProfile()
		if e.Prepare(context.Background(), p) != nil {
			t.Fatal("prepare fixture failed")
		}
		if _, err := e.ProbeExit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if e.SetBusinessGate(true) != nil {
			t.Fatal("proof fixture failed")
		}
		pc := protocolconfig.Config{Version: 1, Type: "https", HTTP: &protocolconfig.HTTP{}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}
		p.Protocol, p.ProtocolConfig = pc.Type, &pc
		p.Auth = engine.Auth{Username: "conflict", Password: "conflict"}
		var err error
		if explicit {
			err = e.PrepareProtocol(context.Background(), p, pc)
		} else {
			err = e.Prepare(context.Background(), p)
		}
		e.mu.Lock()
		runtime, open := e.runtime, e.gate.isOpen()
		e.mu.Unlock()
		if err != protocolconfig.ErrInvalidConfig || runtime != nil || e.VerifiedIPv4() != "" || open {
			t.Fatal("prepare admitted ambiguous credentials or retained old session")
		}
	}
}

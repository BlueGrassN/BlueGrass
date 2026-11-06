package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/engine"
)

// Exercise the wire boundary, not merely an in-process struct field assignment.
func protocolRequestJSON(t *testing.T, kind, config string) []byte {
	t.Helper()
	r := request(Connect, "typed", 1)
	r.Params.Profile.Protocol = kind
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		t.Fatal("invalid fixture")
	}
	p := root["params"].(map[string]any)["profile"].(map[string]any)
	if config != "" {
		p["protocolConfig"] = json.RawMessage(config)
	}
	data, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type typedCapture struct {
	*fakeEngine
	prepared chan engine.Profile
}

func (f *typedCapture) Prepare(ctx context.Context, p engine.Profile) error {
	f.prepared <- p
	return f.fakeEngine.Prepare(ctx, p)
}

func TestControllerDetachesTypedSecretsAndIncludesThemInReplayDigest(t *testing.T) {
	f := &typedCapture{fakeEngine: &fakeEngine{probes: make(chan probeCall, 1), shutdown: make(chan struct{})}, prepared: make(chan engine.Profile, 1)}
	c, err := New(f, &fakeClock{now: baseTime, epoch: "runtime-a"}, "runtime-a", owner())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := DecodeRequest(protocolRequestJSON(t, "https", httpsConfig))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, c, r)
	var captured engine.Profile
	select {
	case captured = <-f.prepared:
	case <-time.After(time.Second):
		t.Fatal("typed profile not dispatched")
	}
	r.Params.Profile.ProtocolConfig.HTTP.Password = "caller-mutated"
	r.Params.Profile.ProtocolConfig.TLS.ALPN[0] = "h2"
	if captured.ProtocolConfig.HTTP.Password != "fixture-password" || captured.ProtocolConfig.TLS.ALPN[0] != "http/1.1" {
		t.Fatal("caller changed accepted credentials/TLS policy")
	}
	a, err := c.Apply(context.Background(), r)
	if err != nil || a.ErrorCode != "REQUEST_CONFLICT" {
		t.Fatal("changed typed credentials replayed prior receipt")
	}
	success(nextProbe(t, f.fakeEngine))
	waitState(t, c, "connected")
}

const httpsConfig = `{"version":1,"type":"https","tls":{"serverName":"entry.example","alpn":["http/1.1"]},"http":{"username":"fixture-user","password":"fixture-password"}}`

func TestTypedProtocolWireConnectsBeforeExitObservation(t *testing.T) {
	c, f, _ := setup(t, false)
	r, err := DecodeRequest(protocolRequestJSON(t, "https", httpsConfig))
	if err != nil {
		t.Fatal("typed protocol rejected by wire decoder", err)
	}
	if code := ValidateProfile(*r.Params.Profile, baseTime); code != "" {
		t.Fatal("valid typed protocol rejected", code)
	}
	apply(t, c, r)
	probe := nextProbe(t, f)
	if !c.Snapshot().BusinessGateOpen {
		t.Fatal("typed profile waited for exit observation")
	}
	probe.reply <- engine.ProbeResult{IPv4: "1.1.1.1", Authenticated: true, Reachable: true}
	c.workers.Wait()
	s := waitState(t, c, "connected")
	if !s.BusinessGateOpen || s.ErrorCode != "" || s.VerifiedExitIPv4 != "1.1.1.1" {
		t.Fatal("typed protocol did not retain its observed exit")
	}
}

func TestExplicitProtocolConfigCannotFallBackToLegacyAuth(t *testing.T) {
	for _, tc := range []struct{ kind, config string }{
		{"vless", ""},
		{"https", `{"version":1,"type":"http","http":{}}`},
		{"socks5", `{"version":1,"type":"https","tls":{"serverName":"entry.example"},"http":{}}`},
		{"https", `{"version":1,"type":"https","http":{}}`},
		{"https", `null`},
		{"https", `{"version":1,"type":"https","tls":{"serverName":"entry.example","insecure":true},"http":{}}`},
	} {
		c, f, _ := setup(t, false)
		r, err := DecodeRequest(protocolRequestJSON(t, tc.kind, tc.config))
		if err == nil {
			a, applyErr := c.Apply(context.Background(), r)
			if applyErr != nil || a.Disposition != "rejected" {
				t.Fatal("incomplete or mismatched config accepted")
			}
		}
		if len(f.history()) != 0 {
			t.Fatal("rejected typed config touched engine")
		}
	}
	// Legacy remains accepted only for SOCKS5 with valid paired RFC1929 fields.
	for _, auth := range []string{`{"username":"","password":""}`, `{"username":"user","password":"password"}`} {
		data := string(protocolRequestJSON(t, "socks5", ""))
		data = strings.Replace(data, `"auth":{"password":"","username":""}`, `"auth":`+auth, 1)
		r, err := DecodeRequest([]byte(data))
		if err != nil || ValidateProfile(*r.Params.Profile, baseTime) != "" {
			t.Fatal("legacy SOCKS regression")
		}
	}
}

func TestControllerRejectsConflictingLegacyAuthBeforeEngine(t *testing.T) {
	for _, tc := range []struct {
		kind, config string
		auth         engine.Auth
	}{
		{"https", httpsConfig, engine.Auth{Username: "fixture-user", Password: "fixture-password"}},
		{"https", httpsConfig, engine.Auth{Password: "legacy-password"}},
		{"socks5", `{"version":1,"type":"socks5","socks5":{"username":"fixture-user","password":"fixture-password"}}`, engine.Auth{Username: "fixture-user", Password: "conflict"}},
		{"socks5", `{"version":1,"type":"socks5","socks5":{}}`, engine.Auth{Username: "legacy-user", Password: "legacy-password"}},
	} {
		t.Run(tc.kind+"-"+tc.auth.Username, func(t *testing.T) {
			c, f, _ := setup(t, false)
			r, err := DecodeRequest(protocolRequestJSON(t, tc.kind, tc.config))
			if err != nil {
				t.Fatal(err)
			}
			// Direct controller callers must get the same credential policy as IPC.
			r.Params.Profile.Auth = tc.auth
			if code := ValidateProfile(*r.Params.Profile, baseTime); code != "ELIG-005" {
				t.Fatalf("conflicting credentials validation = %q", code)
			}
			a, err := c.Apply(context.Background(), r)
			if err != nil || a.Disposition != "rejected" || a.ErrorCode != "ELIG-005" {
				t.Fatal("direct controller accepted conflicting credential sources")
			}
			if len(f.history()) != 0 {
				t.Fatal("rejected profile touched engine")
			}
		})
	}
}

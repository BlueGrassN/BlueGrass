package localengine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/protocolconfig"
)

func TestLocalTypedPrepareReplacesNativeClientWithoutProbePrerequisite(t *testing.T) {
	e := probeLocal(t)
	p := engine.Profile{Protocol: "trojan", Mode: "system_proxy", Entry: engine.Entry{Host: "8.8.8.8", Port: 443}, ExpectedExitIPv4: "1.1.1.1"}
	c := protocolconfig.Config{Version: 1, Type: "trojan", Trojan: &protocolconfig.Trojan{Password: "fixture-password", Transport: "tcp"}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}
	if e.Prepare(context.Background(), p) == nil {
		t.Fatal("inferred non-SOCKS legacy config")
	}
	if err := e.PrepareProtocol(context.Background(), p, c); err != nil {
		t.Fatal(err)
	}
	if e.dial == nil || e.native == nil || e.gate || e.SetBusinessGate(true) != nil {
		t.Fatal("typed prepare missing native ownership or leaked gate")
	}
	if _, err := e.acceptProbe(context.Background(), e.generation, e.session, p.ExpectedExitIPv4); err != nil {
		t.Fatal(err)
	}
	if e.SetBusinessGate(true) != nil {
		t.Fatal("fresh proof rejected")
	}
	old := e.native
	if err := e.SetBusinessGate(false); err != nil {
		t.Fatal(err)
	}
	if e.native != nil || e.dial != nil || e.gate {
		t.Fatal("closed client retained")
	}
	if err := e.PrepareProtocol(context.Background(), p, c); err != nil {
		t.Fatal(err)
	}
	if e.native == old || e.VerifiedIPv4() != "" || e.SetBusinessGate(true) != nil {
		t.Fatal("retired client/observation reused or new gate failed")
	}
	c.Type = "vless"
	if e.PrepareProtocol(context.Background(), p, c) == nil || e.native != nil || e.dial != nil {
		t.Fatal("failed prepare retained client")
	}
}

type closingNative struct {
	calls    atomic.Int32
	closeErr error
}

func (n *closingNative) Close() error { n.calls.Add(1); return n.closeErr }

func TestNativeClientRetirementClosesExactlyOnceAndFailsClosed(t *testing.T) {
	for _, stop := range []string{"block", "stop", "changed-observation", "failed-prepare"} {
		t.Run(stop, func(t *testing.T) {
			e := probeLocal(t)
			n := &closingNative{}
			e.native = n
			switch stop {
			case "block":
				_ = e.SetBusinessGate(false)
			case "stop":
				_ = e.Stop(context.Background())
			case "changed-observation":
				_, _ = e.acceptProbe(context.Background(), e.generation, e.session, "9.9.9.9")
				if n.calls.Load() != 0 || !e.gate {
					t.Fatal("observation retired native client")
				}
			case "failed-prepare":
				_ = e.Prepare(context.Background(), engine.Profile{})
			}
			_ = e.Stop(context.Background())
			if n.calls.Load() != 1 || e.native != nil || e.dial != nil || e.gate {
				t.Fatal("client survived retirement or closed twice")
			}
		})
	}
	e := probeLocal(t)
	e.native = &closingNative{closeErr: errors.New("fixture-secret")}
	if err := e.SetBusinessGate(false); err == nil || err.Error() != "ENGINE_STOP_FAILED" {
		t.Fatal("native cleanup failure hidden or leaked")
	}
	p := engine.Profile{Protocol: "socks5", Mode: "system_proxy", Entry: engine.Entry{Host: "8.8.8.8", Port: 443}, ExpectedExitIPv4: "1.1.1.1"}
	if e.Prepare(context.Background(), p) == nil || e.dial != nil {
		t.Fatal("unconfirmed native teardown allowed replacement")
	}
}

func TestLocalCloseCannotCreateAnotherNativeSession(t *testing.T) {
	e, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	p := engine.Profile{Protocol: "trojan", Mode: "system_proxy", Entry: engine.Entry{Host: "8.8.8.8", Port: 443}, ExpectedExitIPv4: "1.1.1.1",
		ProtocolConfig: &protocolconfig.Config{Version: 1, Type: "trojan", Trojan: &protocolconfig.Trojan{Password: "fixture-password", Transport: "tcp"}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}}
	if e.Prepare(context.Background(), p) == nil || e.native != nil {
		t.Fatal("terminal Close admitted a new native session")
	}
}

func TestLocalBothPreparePathsRejectConflictingAuthAndRetireProof(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		e := probeLocal(t)
		pc := protocolconfig.Config{Version: 1, Type: "https", HTTP: &protocolconfig.HTTP{}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}
		p := engine.Profile{Protocol: pc.Type, ProtocolConfig: &pc, Auth: engine.Auth{Username: "conflict", Password: "conflict"}, Mode: "system_proxy", Entry: engine.Entry{Host: "8.8.8.8", Port: 443}, ExpectedExitIPv4: "1.1.1.1"}
		var err error
		if explicit {
			err = e.PrepareProtocol(context.Background(), p, pc)
		} else {
			err = e.Prepare(context.Background(), p)
		}
		if err != protocolconfig.ErrInvalidConfig || e.native != nil || e.dial != nil || e.gate || e.VerifiedIPv4() != "" {
			t.Fatal("prepare admitted ambiguous credentials or retained old session")
		}
	}
}

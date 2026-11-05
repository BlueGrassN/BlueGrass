package localengine

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"bluegrass/native/core/clock"
	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/protocolconfig"
)

// Only replace external exit evidence. Prepare, native constructors, controller
// dispatch, gate changes and teardown are the real implementations.
type controllerLocal struct {
	*Local
	reached chan struct{}
	reply   chan string
}

func (e *controllerLocal) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	e.mu.Lock()
	epoch, session := e.generation, e.session
	e.mu.Unlock()
	close(e.reached)
	select {
	case ip := <-e.reply:
		return e.acceptProbe(ctx, epoch, session, ip)
	case <-ctx.Done():
		return engine.ProbeResult{}, ctx.Err()
	}
}

func TestControllerWireToRealLocalPrepareAllProtocols(t *testing.T) {
	data, err := os.ReadFile("../protocolconfig/testdata/valid-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []json.RawMessage
	if json.Unmarshal(data, &cases) != nil {
		t.Fatal("invalid fixture")
	}
	for _, raw := range cases {
		var kind struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &kind)
		t.Run(kind.Type, func(t *testing.T) {
			e, err := New(0)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			wrapper := &controllerLocal{Local: e, reached: make(chan struct{}), reply: make(chan string, 1)}
			owner := control.Owner{Environment: "test", DataSource: "api", AccountKey: "fixture-account", ClientEpoch: "fixture-client"}
			c, err := control.New(wrapper, clock.New("fixture-runtime", time.Now().UTC(), clock.Server), "fixture-runtime", owner)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			key := engine.ResourceKey{Environment: owner.Environment, DataSource: owner.DataSource, AccountKey: owner.AccountKey, Kind: "subscription", SourceID: "fixture-resource"}
			x := control.ControlContext{SchemaVersion: 1, RuntimeEpoch: "fixture-runtime", ClientEpoch: owner.ClientEpoch, LogicalSessionID: "fixture-session", AttemptID: "fixture-attempt", ResourceKey: key}
			p := map[string]any{"key": key, "protocol": kind.Type, "entry": engine.Entry{Host: "8.8.8.8", Port: 443}, "auth": engine.Auth{}, "protocolConfig": raw, "validUntilUtc": nil, "configVersion": 1, "configFingerprint": "fixture", "mode": "system_proxy"}
			wire, _ := json.Marshal(map[string]any{"context": x, "requestId": "fixture", "sequence": 1, "command": control.Connect, "params": map[string]any{"profile": p}})
			r, err := control.DecodeRequest(wire)
			if err != nil {
				t.Fatal("typed wire rejected", err)
			}
			ack, err := c.Apply(context.Background(), r)
			if err != nil || ack.Disposition != "applied" {
				t.Fatal("controller rejected typed profile", ack.ErrorCode, err)
			}
			if kind.Type == "tuic" || kind.Type == "anytls" {
				deadline := time.Now().Add(2 * time.Second)
				for c.Snapshot().Connection != "failed" && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if c.Snapshot().BusinessGateOpen || e.native != nil || c.Snapshot().Connection != "failed" {
					t.Fatal("unsupported Xray protocol opened a native session")
				}
				return
			}
			select {
			case <-wrapper.reached:
			case <-time.After(2 * time.Second):
				t.Fatal("real Prepare failed", c.Snapshot().ErrorCode)
			}
			e.mu.Lock()
			native, open := e.native, e.gate
			e.mu.Unlock()
			_, actualXray := native.(*protocolconfig.Outbound)
			if !actualXray || !open || !c.Snapshot().BusinessGateOpen || c.Snapshot().Connection != "connected" || e.VerifiedIPv4() != "" {
				t.Fatal("wrong native type or connection waited for detector")
			}
			wrapper.reply <- "1.1.1.1"
			deadline := time.After(2 * time.Second)
			for e.VerifiedIPv4() != "1.1.1.1" {
				select {
				case <-deadline:
					t.Fatal("post-connect observation failed", c.Snapshot().ErrorCode)
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if e.VerifiedIPv4() != "1.1.1.1" {
				t.Fatal("controller did not commit real engine gate")
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			native, open = e.native, e.gate
			e.mu.Unlock()
			if native != nil || open {
				t.Fatal("controller shutdown retained native client")
			}
		})
	}
}

package tunengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"bluegrass/native/core/clock"
	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/protocolconfig"
)

func TestControllerProfileToTunConfigAllProtocols(t *testing.T) {
	data, err := os.ReadFile("../protocolconfig/testdata/valid-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []protocolconfig.Config
	if json.Unmarshal(data, &cases) != nil {
		t.Fatal("fixture invalid")
	}
	for _, pc := range cases {
		t.Run(pc.Type, func(t *testing.T) {
			entered, reply := make(chan struct{}), make(chan string, 1)
			runtime := &fakeRuntime{probe: func(ctx context.Context) (string, error) {
				close(entered)
				select {
				case ip := <-reply:
					return ip, nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}}
			e := newTestEngine(t, runtime)
			owner := control.Owner{Environment: "test", DataSource: "api", AccountKey: "fixture-account", ClientEpoch: "fixture-client"}
			c, err := control.New(e, clock.New("fixture-runtime", time.Now().UTC(), clock.Server), "fixture-runtime", owner)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			p := testProfile()
			p.ExpectedExitIPv4 = ""
			p.Protocol = pc.Type
			p.ProtocolConfig = &pc
			p.ConfigVersion = 1
			p.ConfigFingerprint = "fixture"
			p.Key = engine.ResourceKey{Environment: owner.Environment, DataSource: owner.DataSource, AccountKey: owner.AccountKey, Kind: "subscription", SourceID: "fixture-resource"}
			x := control.ControlContext{SchemaVersion: 1, ClientEpoch: owner.ClientEpoch, RuntimeEpoch: "fixture-runtime", LogicalSessionID: "fixture-session", AttemptID: "fixture-attempt", ResourceKey: p.Key}
			ack, err := c.Apply(context.Background(), control.ControlRequest{Context: x, RequestID: "fixture", Sequence: 1, Command: control.Connect, Params: control.Parameters{Profile: &p}})
			if errors.Is(pc.Supported(), protocolconfig.ErrUnsupportedProtocol) {
				// Connect acknowledges scheduling; Prepare publishes rejection later.
				deadline := time.After(time.Second)
				for c.Snapshot().Connection != "failed" {
					select {
					case <-entered:
						t.Fatal("unsupported Xray protocol reached runtime probe")
					case <-deadline:
						t.Fatal("unsupported Xray protocol was not rejected", c.Snapshot())
					default:
						time.Sleep(time.Millisecond)
					}
				}
				if c.Snapshot().ErrorCode == "" || e.InterfaceLUID() != 0 {
					t.Fatal("unsupported protocol activated TUN or hid failure")
				}
				return
			}
			if err != nil || ack.Disposition != "applied" {
				t.Fatal("typed TUN rejected", ack.ErrorCode, err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("TUN prepare not dispatched", c.Snapshot().ErrorCode)
			}
			e.mu.Lock()
			prepared := e.prepared
			e.mu.Unlock()
			want := pc.Type
			if want == "socks5" {
				want = "socks"
			}
			if want == "https" {
				want = "http"
			}
			if want == "hysteria2" {
				want = "hysteria"
			}
			if prepared.selected.Protocol != want || !e.gate.isOpen() || e.InterfaceLUID() == 0 || c.Snapshot().Connection != "connected" || e.VerifiedIPv4() != "" {
				t.Fatal("wrong protocol or connection waited for detector")
			}
			reply <- "1.1.1.1"
			deadline := time.After(time.Second)
			for e.VerifiedIPv4() != "1.1.1.1" {
				select {
				case <-deadline:
					t.Fatal("typed TUN failed observation", c.Snapshot().ErrorCode)
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if e.VerifiedIPv4() != "1.1.1.1" || e.InterfaceLUID() == 0 {
				t.Fatal("post-connect observation lost tunnel")
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			if e.VerifiedIPv4() != "" || e.InterfaceLUID() != 0 {
				t.Fatal("controller left tunnel active")
			}
		})
	}
}

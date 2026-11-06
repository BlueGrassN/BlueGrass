package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
)

func TestEntryDNSIsConnectOnly(t *testing.T) {
	for _, command := range []string{"Hello", "GetStatus", "Pause", "Disconnect", "ReleaseProtection", "ApplyResourceUpdate"} {
		t.Run(command, func(t *testing.T) {
			r, err := Decode([]byte(fmt.Sprintf(`{"id":"dns-on-other-command","command":%q,"entryDns":{}}`, command)))
			if err != nil {
				t.Fatalf("entryDns is not part of the request wire schema: %v", err)
			}
			e := &tunnelStub{active: true}
			h := NewTunnel(e)
			defer h.Close()
			reply := h.Handle(r)
			if reply.OK || reply.Error != "INVALID_PARAMETERS" || e.releaseCalls != 0 || !e.active {
				t.Fatalf("non-Connect policy affected protected state: %+v", reply)
			}
		})
	}
}

type dnsEngine struct {
	tunnelStub
	mu       sync.Mutex
	policy   entrydns.Policy
	setCalls int
	setErr   error
	prepared chan entrydns.Policy
}

func (e *dnsEngine) SetEntryDNS(p entrydns.Policy) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setCalls++
	if e.setErr != nil {
		return e.setErr
	}
	e.policy = p
	return nil
}
func (e *dnsEngine) Prepare(context.Context, engine.Profile) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.prepared <- e.policy
	return nil
}
func dnsRequest(t *testing.T, id, mode string, policy any) Request {
	t.Helper()
	var p engine.Profile
	_ = json.Unmarshal(reviewProfile("dns-resource"), &p)
	p.Mode = mode
	wire := map[string]any{"id": id, "command": "Connect", "profile": p}
	if policy != nil {
		wire["entryDns"] = policy
	}
	data, _ := json.Marshal(wire)
	r, err := Decode(data)
	if err != nil {
		t.Fatalf("DNS request decode: %v", err)
	}
	return r
}
func customDNS() entrydns.Policy {
	return entrydns.Policy{Mode: "custom", Candidates: []entrydns.Candidate{{Address: "9.9.9.9", Port: 5353, Transport: "tcp"}}}
}
func dnsHost(t *testing.T, e TunnelEngine, mode string) *Host {
	t.Helper()
	h := NewTunnel(e)
	if mode == "system_proxy" {
		h.tunnel = nil
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}
func TestEntryDNSReachesEngineBeforePrepareAndOmissionResets(t *testing.T) {
	for _, mode := range []string{"system_proxy", "tun"} {
		t.Run(mode, func(t *testing.T) {
			e := &dnsEngine{prepared: make(chan entrydns.Policy, 2)}
			h := dnsHost(t, e, mode)
			for i, p := range []any{customDNS(), nil} {
				r := h.Handle(dnsRequest(t, fmt.Sprint("dns-", i), mode, p))
				if !r.OK {
					t.Fatal(r.Error)
				}
				want := entrydns.Policy{Mode: "auto"}
				if i == 0 {
					want = customDNS()
				}
				select {
				case got := <-e.prepared:
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("Prepare got %+v want %+v", got, want)
					}
				case <-time.After(time.Second):
					t.Fatal("Prepare did not run")
				}
			}
		})
	}
}
func TestEntryDNSInvalidOrSetterFailureNeverPrepares(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy entrydns.Policy
		setErr error
		calls  int
		code   string
	}{
		{"unknown mode", entrydns.Policy{Mode: "invalid"}, nil, 0, "INVALID_CONFIGURATION"},
		{"empty custom", entrydns.Policy{Mode: "custom"}, nil, 0, "INVALID_CONFIGURATION"},
		{"auto with candidates", entrydns.Policy{Mode: "auto", Candidates: customDNS().Candidates}, nil, 0, "INVALID_CONFIGURATION"},
		{"setter failed", customDNS(), errors.New("private engine failure"), 1, "DISCONNECT_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &dnsEngine{setErr: tc.setErr, prepared: make(chan entrydns.Policy, 1)}
			h := dnsHost(t, e, "tun")
			r := h.Handle(dnsRequest(t, "dns-invalid", "tun", tc.policy))
			if r.OK || r.Error != tc.code || e.setCalls != tc.calls || h.controller != nil {
				t.Fatalf("invalid policy affected engine: %+v calls=%d", r, e.setCalls)
			}
		})
	}
}
func TestEntryDNSUnsupportedEngineRejectsOnlyCustom(t *testing.T) {
	for _, p := range []any{nil, entrydns.Policy{Mode: "auto"}, customDNS()} {
		h := dnsHost(t, &tunnelStub{}, "tun")
		r := h.Handle(dnsRequest(t, "dns-unsupported", "tun", p))
		custom := p != nil && p.(entrydns.Policy).Mode == "custom"
		if r.OK == custom || custom && r.Error != "UNSUPPORTED_COMMAND" {
			t.Fatalf("unsupported setter: %+v", r)
		}
	}
}

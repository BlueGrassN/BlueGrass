package mactun

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestTransactionWriteAheadAndForeignWriter(t *testing.T) {
	b := newFakeBackend()
	m, err := newManager(501, b)
	if err != nil {
		t.Fatal(err)
	}
	route := Route{Prefix: netip.MustParsePrefix("0.0.0.0/1"), Interface: "utun233"}
	b.failSave = true
	if err = m.addRoute(context.Background(), route); err == nil {
		t.Fatal("accepted failed journal")
	}
	if len(b.routes) != 0 {
		t.Fatal("mutated before durable intent")
	}
	b.failSave = false
	if err = m.addRoute(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	foreign := route
	foreign.Interface = "en7"
	b.routes[route.Prefix] = foreign
	if err = m.restore(context.Background()); !errors.Is(err, ErrExternalWriter) {
		t.Fatalf("foreign writer: %v", err)
	}
	if b.routes[route.Prefix] != foreign || len(m.j.Routes) != 1 {
		t.Fatal("overwrote foreign route or discarded recovery")
	}
}

func TestJournalRejectsOwnerAndVersion(t *testing.T) {
	for _, modify := range []func(*Journal){func(j *Journal) { j.OwnerUID = 502 }, func(j *Journal) { j.Version++ }} {
		b := newFakeBackend()
		m, err := newManager(501, b)
		if err != nil {
			t.Fatal(err)
		}
		j := m.j
		modify(&j)
		b.j = &j
		if _, err = newManager(501, b); err == nil {
			t.Fatal("accepted foreign journal")
		}
	}
}

func TestPFPolicyHasNoPersistentFlowBypass(t *testing.T) {
	p := Guard{OwnerUID: 501, Interface: "en0", Forwarding: true, TunnelIndex: 42,
		Permits: []Permit{{Endpoint: netip.MustParseAddrPort("8.8.8.8:443"), Protocol: "tcp", UID: 501}}}
	rules, err := renderGuard(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateGuardRules(rules); err != nil {
		t.Fatal(err)
	}
	p.Permits[0].Protocol = "tcp\npass all"
	if _, err = renderGuard(p); err == nil {
		t.Fatal("accepted injected policy")
	}
}

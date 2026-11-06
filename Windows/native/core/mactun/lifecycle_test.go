package mactun

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestConnectTransactionAndExplicitRelease(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.rules, "bluegrass-tunnel") {
		t.Fatal("prepare opened PF tunnel")
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if !tun.forwarding || !b.enabled || !b.marker || b.j == nil {
		t.Fatal("not committed")
	}
	firstPF := slices.Index(b.events, "write-pf")
	adapter := slices.Index(b.events, "adapter-open")
	dns := slices.Index(b.events, "write-dns")
	if firstPF < 0 || firstPF >= adapter || adapter >= dns {
		t.Fatalf("unsafe order: %v", b.events)
	}
	if got := b.dns["Wi-Fi"]; len(got) != 1 || got[0].String() != "172.31.255.1" {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(b.j)
	for _, secret := range []string{"node-secret", "username", "password", "account", "8.8.8.8:1080"} {
		if secret == "8.8.8.8:1080" {
			continue
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret in journal: %s", secret)
		}
	}
	b.events = nil
	if err = tun.Release(); err != nil {
		t.Fatal(err)
	}
	if b.j != nil || b.marker || b.enabled || len(b.routes) != 0 || len(b.dns["Wi-Fi"]) != 0 {
		t.Fatal("release leaked owned state")
	}
	if slices.Index(b.events, "write-dns") > slices.Index(b.events, "release-pf") || slices.Index(b.events, "delete-route") > slices.Index(b.events, "release-pf") {
		t.Fatal("released guard before restore")
	}
}

func TestPauseRevokesSessionPermitsRetainsIntent(t *testing.T) {
	tun, b, f, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("9.9.9.9:50000")
	if err = f.config.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.rules, "50000") {
		t.Fatal("relay not authorized")
	}
	if err = tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.rules, "50000") || strings.Contains(b.rules, "bluegrass-tunnel") || strings.Contains(b.rules, "1080") {
		t.Fatal("pause retained forwarding permits")
	}
	if !tun.Active() || !b.enabled || !b.marker || b.j == nil {
		t.Fatal("pause released intent")
	}
	if f.config.AllowUDPRelay(relay) == nil {
		t.Fatal("stale callback admitted")
	}
	if err = f.config.ReleaseUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if err = tun.Close(); err != nil {
		t.Fatal(err)
	}
	if !b.enabled || b.j == nil {
		t.Fatal("process close released protection")
	}
}

func TestConnectFailureNeverClaimsForwarding(t *testing.T) {
	for _, step := range []string{"check-pf", "compile-pf", "write-pf", "enable-pf", "verify-pf", "add-route", "engine-prepare", "adapter-open", "verify-adapter", "resolvers", "write-dns"} {
		t.Run(step, func(t *testing.T) {
			tun, b, _, err := fixture()
			if err != nil {
				t.Fatal(err)
			}
			b.fail = step
			err = tun.Prepare(context.Background(), profile())
			if err == nil {
				err = tun.SetBusinessGate(true)
			}
			if err == nil || tun.forwarding {
				t.Fatalf("accepted failure: %v", err)
			}
			if b.j != nil && b.j.Active && !b.marker && step != "write-pf" {
				t.Fatal("lost marker")
			}
			if slices.Contains(b.events, "release-pf") || slices.Contains(b.events, "delete-journal") {
				t.Fatal("failure released protection")
			}
		})
	}
}

func TestCrashRecoveryDoesNotResumeForwarding(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	// Model the old process being gone without calling Close/Release.
	tun.current.Load().cancel()
	tun.current.Store(nil)
	recovered, err := newTunnel(501, b)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Active() || recovered.forwarding || strings.Contains(b.rules, "bluegrass-tunnel") {
		t.Fatal("recovered forwarding")
	}
	id, err := recovered.ResumeLogicalSession(profile().Key)
	if err != nil || id != "session_123" {
		t.Fatalf("lost continuity: %s %v", id, err)
	}
	if err = recovered.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestForeignDNSPreventsReleaseAndKeepsJournal(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	foreign := []netip.Addr{netip.MustParseAddr("4.4.4.4")}
	b.dns["Wi-Fi"] = foreign
	if err = tun.Release(); !errors.Is(err, ErrExternalWriter) {
		t.Fatal(err)
	}
	if !sameAddresses(b.dns["Wi-Fi"], foreign) || b.j == nil || !b.enabled || !b.marker {
		t.Fatal("clobbered external writer or lost recovery")
	}
}

func TestReferenceCountAndSplitPolicy(t *testing.T) {
	tun, _, f, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if f.config.AllowDirect != nil {
		t.Fatal("global admits split")
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	e := netip.MustParseAddrPort("9.9.9.9:4444")
	if err = f.config.AllowUDPRelay(e); err != nil {
		t.Fatal(err)
	}
	if err = f.config.AllowUDPRelay(e); err != nil {
		t.Fatal(err)
	}
	if err = f.config.ReleaseUDPRelay(e); err != nil {
		t.Fatal(err)
	}
	if tun.current.Load().permits[Permit{Endpoint: e, Protocol: "udp"}] != 1 {
		t.Fatal("permit refcount")
	}
	if err = tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalIdentityAndControlValidation(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	wrong := profile().Key
	wrong.AccountKey = "other"
	if _, err = tun.ResumeLogicalSession(wrong); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if err = tun.BindLogicalSession(wrong, "x"); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost", "host.local", "x.home.arpa", "-x.example.com", "EXAMPLE.COM", "good.example\npass all", "x.onion"} {
		before := len(b.events)
		if _, err = tun.ResolveControlHost(context.Background(), host); err == nil {
			t.Fatal(host)
		}
		if before != len(b.events) {
			t.Fatal("invalid host touched backend")
		}
	}
	if !validControlHost("client-api.sunip.vip") {
		t.Fatal("public host rejected")
	}
}

func TestSplitDirectIsRootScopedAndRetiredOnPause(t *testing.T) {
	tun, b, f, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	p := profile()
	p.RoutingMode = "bypass_cn"
	if err = tun.Prepare(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if f.config.AllowDirect == nil || f.config.ReleaseDirect == nil {
		t.Fatal("split callbacks missing")
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	e := netip.MustParseAddrPort("114.114.114.114:8080")
	if err = f.config.AllowDirect("tcp", e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.rules, "port 8080 user 0") || strings.Contains(b.rules, "port 8080 user 501") {
		t.Fatal("direct permission not root scoped")
	}
	if err = tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.rules, "8080") {
		t.Fatal("paused direct remains")
	}
	if err = f.config.AllowDirect("tcp", e); err == nil {
		t.Fatal("retired direct callback allowed")
	}
}

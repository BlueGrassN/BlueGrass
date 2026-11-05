package localengine

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/exitprobe"
)

func TestObservationLocalGateBeforeProbeIgnoresLegacyExit(t *testing.T) {
	for _, legacy := range []string{"", "not-an-address", "9.9.9.9"} {
		t.Run(legacy, func(t *testing.T) {
			e := probeLocal(t)
			p := engine.Profile{Protocol: "socks5", Mode: "system_proxy", Entry: engine.Entry{Host: "8.8.8.8", Port: 443}, ExpectedExitIPv4: legacy}
			if err := e.Prepare(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if e.VerifiedIPv4() != "" || e.gate {
				t.Fatal("prepare retained observation/open gate")
			}
			if err := e.SetBusinessGate(true); err != nil {
				t.Fatal("prepared engine needs no detector", err)
			}
			if !e.gate || e.VerifiedIPv4() != "" {
				t.Fatal("gate fabricated observation")
			}
		})
	}
}

func TestObservationLocalFailureAndChangedIPPreserveForwarding(t *testing.T) {
	e := probeLocal(t)
	peer, conn := net.Pipe()
	defer peer.Close()
	e.conns[conn] = struct{}{}
	if _, err := e.ProbeExit(context.Background()); err == nil {
		t.Fatal("failed detector succeeded")
	}
	for _, ip := range []string{"9.9.9.9", "8.8.4.4"} {
		if _, err := e.acceptProbe(context.Background(), e.generation, e.session, ip); err != nil {
			t.Fatal(err)
		}
		if !e.gate || e.VerifiedIPv4() != ip {
			t.Fatal("changed observation revoked forwarding")
		}
		_ = peer.SetDeadline(time.Now().Add(time.Second))
		done := make(chan error, 1)
		go func() { _, err := conn.Write([]byte{1}); done <- err }()
		if _, err := io.ReadFull(peer, make([]byte, 1)); err != nil {
			t.Fatal("active stream closed", err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestIPv4OnlyLocalRejectsIPv6WithoutClosingGate(t *testing.T) {
	e := probeLocal(t)
	peer, conn := net.Pipe()
	defer peer.Close()
	e.conns[conn] = struct{}{}
	if _, err := e.acceptProbe(context.Background(), e.generation, e.session, "2606:4700::1111"); err == nil {
		t.Fatal("IPv6 accepted")
	}
	report := exitprobe.Diagnostic(context.Background(), diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "fixture"}, "", func(context.Context) (string, error) { return "2606:4700::1111", nil })
	if _, err := e.finishDiagnostic(context.Background(), e.generation, e.session, report); err != nil {
		t.Fatal(err)
	}
	if !e.gate || e.VerifiedIPv4() != "1.1.1.1" {
		t.Fatal("invalid observation changed established session")
	}
	_ = peer.SetDeadline(time.Now().Add(time.Second))
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte{1}); done <- err }()
	if _, err := io.ReadFull(peer, make([]byte, 1)); err != nil {
		t.Fatal("IPv6 response closed TCP", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

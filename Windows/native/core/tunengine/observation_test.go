package tunengine

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"bluegrass/native/core/diagnostics"
)

func TestObservationTunGateBeforeProbeIgnoresLegacyExit(t *testing.T) {
	for _, legacy := range []string{"", "not-an-address", "1.1.1.1"} {
		t.Run(legacy, func(t *testing.T) {
			r := &fakeRuntime{probe: func(context.Context) (string, error) { t.Fatal("gate invoked detector"); return "", nil }}
			e := newTestEngine(t, r)
			p := testProfile()
			p.ExpectedExitIPv4 = legacy
			if err := e.Prepare(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if err := e.SetBusinessGate(true); err != nil {
				t.Fatal("prepared engine needs no detector", err)
			}
			if e.InterfaceLUID() == 0 || !e.gate.isOpen() || e.VerifiedIPv4() != "" || r.enableSawOpen {
				t.Fatal("activation/observation boundary")
			}
		})
	}
}

func TestObservationTunFailureChangedIPAndCallerCancellationKeepForwarding(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	_, _ = e.ProbeExit(context.Background()) // old implementations can reach later regression assertions
	if err := e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	peer, conn := net.Pipe()
	defer peer.Close()
	if !e.gate.track(conn) {
		t.Fatal("track")
	}
	packet := &packetFixture{closed: make(chan struct{}), receive: make(chan []byte, 1)}
	if !e.gate.track(packet) {
		t.Fatal("track UDP")
	}
	r.probe = func(context.Context) (string, error) { return "", errors.New("fixture failure") }
	if _, err := e.ProbeExit(context.Background()); err == nil {
		t.Fatal("failed detector succeeded")
	}
	for _, ip := range []string{"1.1.1.1", "8.8.4.4"} {
		r.probe = func(context.Context) (string, error) { return ip, nil }
		if _, err := e.ProbeExit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !e.gate.isOpen() || e.VerifiedIPv4() != ip || e.InterfaceLUID() == 0 {
			t.Fatal("observation revoked forwarding")
		}
		select {
		case <-packet.closed:
			t.Fatal("observation closed UDP")
		default:
		}
		_ = peer.SetDeadline(time.Now().Add(time.Second))
		done := make(chan error, 1)
		go func() { _, err := conn.Write([]byte{1}); done <- err }()
		if _, err := io.ReadFull(peer, make([]byte, 1)); err != nil {
			t.Fatal("stream closed", err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ProbeExit(ctx); err == nil {
		t.Fatal("cancelled probe accepted")
	}
	if !e.gate.isOpen() || e.InterfaceLUID() == 0 {
		t.Fatal("caller cancellation retired engine")
	}
}

func TestIPv4OnlyTunRejectsIPv6WithoutClosingGate(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	if err := e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	_, _ = e.ProbeExit(context.Background())
	peer, conn := net.Pipe()
	defer peer.Close()
	packet := &packetFixture{closed: make(chan struct{}), receive: make(chan []byte, 1)}
	if !e.gate.track(conn) || !e.gate.track(packet) {
		t.Fatal("track")
	}
	r.probe = func(context.Context) (string, error) { return "2606:4700::1111", nil }
	if _, err := e.ProbeExit(context.Background()); err == nil {
		t.Fatal("IPv6 accepted")
	}
	report, err := e.Diagnose(context.Background(), diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Results {
		if result.ID == "proxy_exit" && (result.Status != diagnostics.Failed || result.ObservedIP != "") {
			t.Fatal("IPv6 diagnostic passed", result)
		}
	}
	if !e.gate.isOpen() || e.InterfaceLUID() == 0 || e.VerifiedIPv4() != "9.9.9.9" {
		t.Fatal("invalid observation changed established session")
	}
	select {
	case <-packet.closed:
		t.Fatal("IPv6 response closed UDP")
	default:
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

func TestObservationTunCancelledProbeAndSnapshotErrorPreserveSession(t *testing.T) {
	for _, diagnostic := range []bool{false, true} {
		for _, failure := range []string{"cancel-before", "cancel-during", "snapshot-error", "invalid-response"} {
			t.Run(failure+map[bool]string{false: "/probe", true: "/diagnostic"}[diagnostic], func(t *testing.T) {
				r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
				e := newTestEngine(t, r)
				if err := e.Prepare(context.Background(), testProfile()); err != nil {
					t.Fatal(err)
				}
				if err := e.SetBusinessGate(true); err != nil {
					t.Fatal(err)
				}
				_, _ = e.ProbeExit(context.Background())
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch failure {
				case "cancel-before":
					cancel()
				case "cancel-during":
					r.probe = func(context.Context) (string, error) { cancel(); return "1.1.1.1", nil }
				case "snapshot-error":
					e.config.Network = func(context.Context) (NetworkSnapshot, error) {
						return NetworkSnapshot{}, errors.New("fixture snapshot failure")
					}
				case "invalid-response":
					r.probe = func(context.Context) (string, error) { return "invalid", nil }
				}
				if diagnostic {
					report, err := e.Diagnose(ctx, diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "fixture"})
					if err == nil && failure != "invalid-response" {
						t.Fatal("cancelled/unavailable observation accepted")
					}
					for _, result := range report.Results {
						if result.ID == "proxy_exit" && (result.Status == diagnostics.Passed || result.ObservedIP != "") {
							t.Fatal("failed observation passed")
						}
					}
				} else if _, err := e.ProbeExit(ctx); err == nil {
					t.Fatal("invalid/cancelled probe accepted")
				}
				if !e.gate.isOpen() || e.InterfaceLUID() == 0 || e.VerifiedIPv4() != "9.9.9.9" {
					t.Fatal("detector failure affected healthy session")
				}
			})
		}
	}
}

func TestObservationTunActualPhysicalNetworkChangeStillRetiresSession(t *testing.T) {
	for _, diagnostic := range []bool{false, true} {
		r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
		e := newTestEngine(t, r)
		if err := e.Prepare(context.Background(), testProfile()); err != nil {
			t.Fatal(err)
		}
		if err := e.SetBusinessGate(true); err != nil {
			t.Fatal(err)
		}
		provider := e.config.Network
		r.probe = func(context.Context) (string, error) {
			e.config.Network = func(ctx context.Context) (NetworkSnapshot, error) {
				n, err := provider(ctx)
				n.Generation++
				return n, err
			}
			return "1.1.1.1", nil
		}
		if diagnostic {
			_, _ = e.Diagnose(context.Background(), diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "fixture"})
		} else {
			_, _ = e.ProbeExit(context.Background())
		}
		if e.gate.isOpen() || e.InterfaceLUID() != 0 || e.VerifiedIPv4() != "" {
			t.Fatal("physical change kept obsolete session")
		}
	}
}

func TestObservationCacheNamespaceIgnoresLegacyExit(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	key := mappingNamespace(p)
	for _, value := range []string{"", "1.1.1.1", "malformed"} {
		p.profile.ExpectedExitIPv4 = value
		if mappingNamespace(p) != key {
			t.Fatal("observation metadata invalidated FakeIP cache")
		}
	}
	p.network.Generation++
	if mappingNamespace(p) == key {
		t.Fatal("physical network identity lost")
	}
}

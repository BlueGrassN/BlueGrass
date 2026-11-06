package protocolconfig

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestEveryPinnedConstructorInitializesWithoutDial(t *testing.T) {
	for _, c := range fixtures() {
		t.Run(c.Type, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out, err := NewOutbound(ctx, c, netip.MustParseAddrPort("8.8.8.8:8443"))
			if c.Type == "tuic" || c.Type == "anytls" {
				if !errors.Is(err, ErrUnsupportedProtocol) || out != nil {
					t.Fatalf("unsupported protocol: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Type == "http" || c.Type == "https" {
				if conn, err := out.ListenPacket(ctx, "9.9.9.9:53"); err == nil || conn != nil {
					t.Fatal("HTTP UDP accepted")
				}
			}
			cancel()
			if err := CloseOutbound(out); err != nil {
				t.Fatal(err)
			}
			if conn, err := out.DialContext(context.Background(), "tcp", "example.org:443"); err == nil || conn != nil {
				t.Fatal("retired session dial accepted")
			}
		})
	}
}

func TestAllowedCipherVariantsUsePinnedConstructors(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			password := "fixture-password"
			if strings.HasPrefix(method, "2022-") {
				size := 32
				if method == "2022-blake3-aes-128-gcm" {
					size = 16
				}
				password = base64.StdEncoding.EncodeToString(make([]byte, size))
			}
			out, err := NewOutbound(context.Background(), Config{Version: 1, Type: "shadowsocks", Shadowsocks: &Shadowsocks{Method: method, Password: password}}, netip.MustParseAddrPort("8.8.8.8:8443"))
			if err != nil {
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type nativeCloseSpy struct{ calls int }

func (s *nativeCloseSpy) Close() error {
	s.calls++
	return errors.New("credential-fixture must not escape")
}

func TestNativeCloseAndInvalidErrorsAreSanitized(t *testing.T) {
	var closer io.Closer = &nativeCloseSpy{}
	err := CloseOutbound(closer)
	if err == nil || err.Error() != "PROTOCOL_CLOSE_FAILED" || closer.(*nativeCloseSpy).calls != 1 {
		t.Fatalf("close contract: %v", err)
	}
	if err := CloseOutbound(nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := NewOutbound(ctx, fixtures()[0], netip.MustParseAddrPort("8.8.8.8:443")); err == nil || out != nil {
		t.Fatal("canceled session accepted")
	}
	if out, err := NewOutbound(context.Background(), fixtures()[0], netip.MustParseAddrPort("127.0.0.1:443")); !errors.Is(err, ErrInvalidConfig) || out != nil {
		t.Fatal("public entry guard bypassed")
	}
}

func nativeAwait(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal(what + " did not stop")
	}
}

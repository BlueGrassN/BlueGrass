package auth

import (
	"context"
	"net/netip"
	"testing"

	"bluegrass/client-api/internal/secure"
)

// Uses the existing opt-in isolated MySQL fixture. Local runs without an
// explicitly configured test database skip this engine-level regression.
func TestMySQLLoginIPSurvivesRefreshAndRelogin(t *testing.T) {
	svc, _, store, _ := serviceFixture(t)
	ctx := context.Background()
	in := input()
	legacy, err := svc.Login(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Access(ctx, secure.Digest(legacy.AccessToken))
	if err != nil || old.LoginIP != "" {
		t.Fatalf("missing IP must remain readable: %v", err)
	}
	for _, address := range []string{"198.51.100.10", "2001:db8::2"} {
		in.ClientIP = netip.MustParseAddr(address)
		issued, err := svc.Login(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		requestID, err := secure.UUID()
		if err != nil {
			t.Fatal(err)
		}
		refreshed, err := svc.Refresh(ctx, issued.RefreshToken, requestID)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := svc.Authenticate(ctx, refreshed.AccessToken)
		if err != nil || identity.Session.LoginIP != address {
			t.Fatalf("refresh changed login IP: %v", err)
		}
		old, err = store.Access(ctx, secure.Digest(legacy.AccessToken))
		if err != nil || old.RevokedAt == nil {
			t.Fatalf("relogin must revoke the previous session: %v", err)
		}
		previousIP := ""
		if address == "2001:db8::2" {
			previousIP = "198.51.100.10"
		}
		if old.LoginIP != previousIP {
			t.Fatal("relogin overwrote previous session's IP")
		}
		legacy = refreshed
	}
}

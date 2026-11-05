//go:build darwin

package mactun

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func kernelContext(t *testing.T) context.Context {
	t.Helper()
	if os.Getenv("BLUEGRASS_MACTUN_KERNEL_TESTS") != "1" {
		t.Skip("opt-in ephemeral Darwin root runner only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("kernel test requires root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// No -E/-d/global -f/-F, no default route, no configured service DNS mutation,
// and no production anchor/journal. This test cannot intercept CI control flow.
func TestDarwinKernelIsolatedAnchor(t *testing.T) {
	ctx := kernelContext(t)
	b := &darwinBackend{}
	u, err := b.Underlay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := renderGuard(Guard{OwnerUID: 501, Interface: u.Interface, MDNSInterface: u.Interface, Permits: []Permit{{Endpoint: netip.MustParseAddrPort("192.0.2.123:443"), Protocol: "tcp", UID: 501}}})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := b.CompilePF(ctx, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateGuardRules(canonical); err != nil {
		t.Fatal(err)
	}
	anchor := fmt.Sprintf("bluegrass-kernel-test-%d", os.Getpid())
	before, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-a", anchor, "-sr")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(before) != "" {
		t.Fatal("test anchor already occupied")
	}
	testRule := "block drop out quick inet to 192.0.2.123 label \"bluegrass-kernel-test\"\n"
	expected, _, err := systemCommand(ctx, testRule, "/sbin/pfctl", "-a", anchor, "-o", "none", "-n", "-v", "-f", "-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		current, _, e := systemCommand(cleanup, "", "/sbin/pfctl", "-a", anchor, "-sr")
		if e == nil && strings.TrimSpace(current) == "" {
			return
		}
		if e != nil || canonicalRules(current) != canonicalRules(expected) {
			t.Errorf("test anchor changed externally; refusing cleanup: %v", e)
			return
		}
		if _, _, e = systemCommand(cleanup, "", "/sbin/pfctl", "-a", anchor, "-f", "-"); e != nil {
			t.Error(e)
		}
		empty, _, e := systemCommand(cleanup, "", "/sbin/pfctl", "-a", anchor, "-sr")
		if e != nil || strings.TrimSpace(empty) != "" {
			t.Errorf("anchor cleanup unverified: %v", e)
		}
	})
	_, _, err = systemCommand(ctx, testRule, "/sbin/pfctl", "-a", anchor, "-o", "none", "-f", "-")
	if err != nil {
		t.Fatal(err)
	}
	installed, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-a", anchor, "-sr")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(installed, "192.0.2.123") || !strings.Contains(installed, "bluegrass-kernel-test") {
		t.Fatal("kernel did not retain own rule")
	}
}

func TestDarwinKernelOwnedTestHostRoute(t *testing.T) {
	ctx := kernelContext(t)
	b := &darwinBackend{}
	prefix := netip.MustParsePrefix("198.19.255.254/32")
	before, err := b.ReadRoute(ctx, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if before != nil {
		t.Skip("reserved test host route already occupied")
	}
	expected := Route{Prefix: prefix, Interface: "lo0"}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		current, e := b.ReadRoute(cleanup, prefix)
		if e == nil && current == nil {
			return
		}
		if e != nil || current == nil || *current != expected {
			t.Errorf("test route ownership changed; no delete: %v", e)
			return
		}
		if _, _, e = systemCommand(cleanup, "", "/sbin/route", "-n", "delete", "-host", prefix.Addr().String(), "-interface", "lo0"); e != nil {
			t.Error(e)
		}
		current, e = b.ReadRoute(cleanup, prefix)
		if e != nil || current != nil {
			t.Errorf("route cleanup unverified: %v", e)
		}
	})
	_, _, err = systemCommand(ctx, "", "/sbin/route", "-n", "add", "-host", prefix.Addr().String(), "-interface", "lo0")
	if err != nil {
		t.Fatal(err)
	}
	current, err := b.ReadRoute(ctx, prefix)
	if err != nil || current == nil || *current != expected {
		t.Fatalf("test route not verified: %+v %v", current, err)
	}
}

func TestDarwinKernelReferenceRead(t *testing.T) {
	ctx := kernelContext(t)
	b := &darwinBackend{}
	if _, err := b.TokenExists(ctx, "18446744073709551615"); err != nil {
		t.Fatal(err)
	}
}

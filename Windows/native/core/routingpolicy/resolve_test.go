package routingpolicy

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestResolveUsesCanonicalDNSNameAndRejectsMalformedLabels(t *testing.T) {
	rules, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host != "xn--bcher-kva.de" {
			t.Fatalf("noncanonical lookup %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	if _, err := Resolve(context.Background(), BypassCN, rules, "BÜCHER.DE.", lookup, lookup); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"www.baidu.com..", "-invalid.cn", "bad name.cn"} {
		if _, err := Resolve(context.Background(), BypassCN, rules, host, lookup, lookup); err == nil {
			t.Fatal("malformed DNS target accepted")
		}
	}
}

func TestResolveSeparatesDNSAndPinsTheChosenAddress(t *testing.T) {
	rules, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, host, answer  string
		mode                Mode
		direct, domesticDNS bool
	}{
		{"Chinese domain CDN", "www.baidu.com", "8.8.8.8", BypassCN, true, true},
		{"foreign domain on Chinese IP", "www.google.com", "223.5.5.5", BypassCN, false, false},
		{"unknown domain China IP", "unlisted.bluegrass-fixture.invalid", "223.5.5.5", BypassCN, true, false},
		{"unknown foreign", "unlisted.bluegrass-fixture.invalid", "8.8.8.8", BypassCN, false, false},
		{"global Chinese domain", "www.baidu.com", "223.5.5.5", Global, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var domestic, proxy int
			lookup := func(count *int) Lookup {
				return func(context.Context, string) ([]netip.Addr, error) {
					*count++
					return []netip.Addr{netip.MustParseAddr(tc.answer)}, nil
				}
			}
			route, err := Resolve(context.Background(), tc.mode, rules, tc.host, lookup(&domestic), lookup(&proxy))
			if err != nil || route.Address.String() != tc.answer || route.Direct != tc.direct || domestic+proxy != 1 || (domestic == 1) != tc.domesticDNS {
				t.Fatalf("route/DNS mismatch: %+v %v domestic=%d proxy=%d", route, err, domestic, proxy)
			}
		})
	}
}

func TestResolveRejectsPrivateAnswersWithoutFallback(t *testing.T) {
	rules, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"127.0.0.1", "192.168.1.1", "100.64.0.1", "198.18.0.1", "::1", "2606:4700::1111"} {
		lookup := func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr(value)}, nil
		}
		if _, err := Resolve(context.Background(), BypassCN, rules, "www.baidu.com", lookup, nil); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	failed := func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("fixture") }
	forbidden := func(context.Context, string) ([]netip.Addr, error) {
		t.Fatal("failed domestic DNS fell back to proxy")
		return nil, nil
	}
	if _, err := Resolve(context.Background(), BypassCN, rules, "www.baidu.com", failed, forbidden); err == nil {
		t.Fatal("DNS failure hidden")
	}
}

func TestResolveLiteralAndLegacyGlobalNeedNoDNS(t *testing.T) {
	rules, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Global, BypassCN} {
		route, err := Resolve(context.Background(), mode, rules, "223.5.5.5", nil, nil)
		if err != nil || route.Direct != (mode == BypassCN) {
			t.Fatalf("literal route: %+v %v", route, err)
		}
	}
	if _, err := Resolve(context.Background(), BypassCN, nil, "8.8.8.8", nil, nil); err == nil {
		t.Fatal("missing rules silently accepted")
	}
}

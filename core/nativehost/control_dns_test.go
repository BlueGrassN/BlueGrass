package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

// The trusted engine owns the fixed hostname allowlist. Nativehost must not
// replace this boundary with a general system/entry DNS resolver.
type controlDNSEngine struct {
	tunnelStub
	addresses []netip.Addr
	err       error
	calls     int
	bounded   bool
}

func (e *controlDNSEngine) ResolveControlHost(ctx context.Context, host string) ([]netip.Addr, error) {
	e.calls++
	_, e.bounded = ctx.Deadline()
	if host != "client-api.sunip.vip" {
		return nil, errors.New("untrusted hostname")
	}
	return e.addresses, e.err
}
func controlDNSRequest(t *testing.T, command, host string) Request {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"id": "control-dns", "command": command, "host": host})
	r, err := Decode(b)
	if err != nil {
		t.Fatal("host absent from IPC schema", err)
	}
	return r
}
func TestControlDNSReturnsValidatedAddressesWithProtectionSnapshot(t *testing.T) {
	e := &controlDNSEngine{tunnelStub: tunnelStub{active: true}, addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("9.9.9.9")}}
	h := dnsHost(t, e, "tun")
	r := h.Handle(controlDNSRequest(t, "ResolveControlHost", "client-api.sunip.vip"))
	if !r.OK || !reflect.DeepEqual(r.Snapshot["controlAddresses"], []string{"1.1.1.1", "9.9.9.9"}) || r.Snapshot["protectionIntent"] != true || r.Snapshot["businessGateOpen"] != false || r.Snapshot["actualMode"] != "tun" || !e.bounded || e.calls != 1 || e.releaseCalls != 0 {
		t.Fatalf("DNS result lost protection or addresses: %+v", r)
	}
}
func TestControlDNSRejectsInvalidResultsAndUnknownHosts(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		ips        []netip.Addr
		err        error
	}{
		{name: "empty"},
		{name: "private", ips: []netip.Addr{netip.MustParseAddr("10.0.0.1")}},
		{name: "mixed", ips: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("127.0.0.1")}},
		{name: "ipv6", ips: []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")}},
		{name: "mapped", ips: []netip.Addr{netip.MustParseAddr("::ffff:1.1.1.1")}},
		{name: "invalid", ips: []netip.Addr{{}}},
		{name: "too many", ips: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.1.1.1")}},
		{name: "engine failure", err: errors.New("private engine detail")},
		{name: "unknown hostname", host: "attacker.example", ips: []netip.Addr{netip.MustParseAddr("1.1.1.1")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &controlDNSEngine{tunnelStub: tunnelStub{active: true}, addresses: tc.ips, err: tc.err}
			h := dnsHost(t, e, "tun")
			host := tc.host
			if host == "" {
				host = "client-api.sunip.vip"
			}
			r := h.Handle(controlDNSRequest(t, "ResolveControlHost", host))
			if r.OK || r.Error != "CONTROL_DNS_FAILED" || r.Snapshot["controlAddresses"] != nil || r.Snapshot["protectionIntent"] != true || e.releaseCalls != 0 {
				t.Fatalf("unsafe DNS result: %+v", r)
			}
		})
	}
	h := dnsHost(t, &tunnelStub{active: true}, "tun")
	if r := h.Handle(controlDNSRequest(t, "ResolveControlHost", "client-api.sunip.vip")); r.OK || r.Error != "CONTROL_DNS_FAILED" {
		t.Fatalf("unsupported resolver fell back: %+v", r)
	}
}
func TestControlDNSHostIsCommandSpecific(t *testing.T) {
	for _, command := range []string{"Hello", "GetStatus", "Connect", "Pause", "Disconnect", "ReleaseProtection", "ApplyResourceUpdate"} {
		e := &controlDNSEngine{tunnelStub: tunnelStub{active: true}}
		h := dnsHost(t, e, "tun")
		r := h.Handle(controlDNSRequest(t, command, "client-api.sunip.vip"))
		if r.OK || r.Error != "INVALID_PARAMETERS" || e.calls != 0 || e.releaseCalls != 0 {
			t.Fatalf("host accepted on %s: %+v", command, r)
		}
	}
	e := &controlDNSEngine{tunnelStub: tunnelStub{active: true}}
	h := dnsHost(t, e, "tun")
	if r := h.Handle(controlDNSRequest(t, "ResolveControlHost", "")); r.OK || r.Error != "CONTROL_DNS_FAILED" || e.calls != 0 {
		t.Fatalf("empty hostname dispatched: %+v", r)
	}
}

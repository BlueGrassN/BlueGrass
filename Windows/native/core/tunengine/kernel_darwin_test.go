//go:build darwin

package tunengine

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Real kernel sockets -> utun233 -> pinned Xray native stack/router -> pinned
// Xray freedom -> local echo fixture. No public packets, default-route, DNS
// preference or PF mutation. Redirect is test-only and never app policy.
func TestDarwinKernelUtunXrayTCPUDPDNSAndCleanup(t *testing.T) {
	requireDarwinKernel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	before, err := SystemNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := netip.MustParsePrefix("10.253.233.9/32")
	for _, occupied := range before.Occupied {
		if occupied.Overlaps(target) {
			t.Fatal("test prefix already occupied", occupied)
		}
	}
	v4, err := choosePool([]string{"172.31.255.1/30", "172.30.255.1/30", "10.255.255.1/30"}, before.Occupied)
	if err != nil {
		t.Fatal(err)
	}
	selected := darwinKernelOutboundFixture(t, ctx, "lo0")
	gate := newSessionGate()
	defer gate.retire()
	// Production DNS predefined policy handles the actual TCP/UDP DNS ingress.
	policy, err := decodePolicy(obj{"dns": obj{"rules": []obj{{"domain_regex": []string{`^kernel\.bluegrass\.test\.$`}, "query_type": []string{"A"}, "action": "predefined", "rcode": "NOERROR", "answer": []string{"kernel.bluegrass.test. 60 IN A 10.253.233.9"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	router := &nativeRouter{ctx: ctx, gate: gate, selected: selected, dns: &policyDNS{config: policy}}
	router.config.Route.Final = "selected"
	platform := &nativePlatform{}
	opts := nativeOptions(v4, netip.MustParsePrefix("fdfe:dcba:9876::1/126"))
	device, stack, err := startNative(ctx, platform, opts, router, gate)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	cleanup := func() {
		if closed {
			return
		}
		closed = true
		gate.retire()
		if e := stack.Close(); e != nil {
			t.Error("stack cleanup", e)
		}
		if e := device.Close(); e != nil {
			t.Error("utun cleanup", e)
		}
	}
	defer cleanup()
	iface, err := net.InterfaceByName("utun233")
	if err != nil || iface.Index <= 0 || platform.luid.Load() != uint64(iface.Index) {
		t.Fatal("real kernel index not published", iface, err)
	}
	if other, _, e := openNativeTun(opts); e == nil || e.Error() != "TUN_ADAPTER_ALREADY_EXISTS" {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("existing adapter reused")
	}
	// Route only this private test host. Refuse an existing exact route first.
	routeCmd := func(args ...string) error {
		c := exec.CommandContext(ctx, "/sbin/route", args...)
		c.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
		out, e := c.CombinedOutput()
		if e != nil {
			t.Logf("route %v: %s", args, out)
		}
		return e
	}
	if err = routeCmd("-n", "add", "-host", target.Addr().String(), "-interface", InterfaceName); err != nil {
		t.Fatal(err)
	}
	routePresent := true
	deleteRoute := func() {
		if !routePresent {
			return
		}
		routePresent = false
		if e := routeCmd("-n", "delete", "-host", target.Addr().String(), "-interface", InterfaceName); e != nil {
			t.Error("private route cleanup", e)
		}
	}
	defer deleteRoute()
	active, err := SystemNetwork(ctx)
	if err != nil || active.Generation != before.Generation {
		t.Fatal("adapter activation changed physical network/resolver state", active.Generation, err)
	}
	routes, err := readSystemRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		if route.LUID == platform.luid.Load() && route.Prefix.Bits() <= 1 {
			t.Fatal("upstream installed a default/split-default route", route.Prefix)
		}
	}
	if !gate.open() {
		t.Fatal("gate refused activation")
	}
	// The app uses ordinary Darwin kernel TCP and UDP, not Stack.DialTCP/DialUDP.
	for _, network := range []string{"tcp4", "udp4"} {
		c, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(target.Addr().String(), "4242"))
		if e != nil {
			t.Fatal(network, e)
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		payload := []byte("BlueGrass real Darwin " + network)
		_, e = c.Write(payload)
		got := make([]byte, len(payload))
		if e == nil {
			_, e = io.ReadFull(c, got)
		}
		_ = c.Close()
		if e != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s packet flow: %q %v", network, got, e)
		}
	}
	for _, network := range []string{"udp", "tcp"} {
		query := new(dns.Msg).SetQuestion("kernel.bluegrass.test.", dns.TypeA)
		client := &dns.Client{Net: network, Timeout: 5 * time.Second}
		reply, _, e := client.ExchangeContext(ctx, query, net.JoinHostPort(v4.Addr().String(), "53"))
		if e != nil {
			t.Fatal(network, "DNS packet flow", e)
		}
		if len(reply.Answer) != 1 {
			t.Fatal("wrong DNS answer count", reply)
		}
		answer, ok := reply.Answer[0].(*dns.A)
		if !ok || answer.A.String() != "10.253.233.9" {
			t.Fatal("wrong DNS result", reply)
		}
	}
	if gate.upload.Load() == 0 || gate.download.Load() == 0 {
		t.Fatal("native packet accounting absent")
	}
	deleteRoute()
	cleanup()
	after, err := SystemNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("network state not restored: %d -> %d", before.Generation, after.Generation)
	}
	for _, p := range after.Occupied {
		if p == target {
			t.Fatal("test route leaked")
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Name == InterfaceName {
			t.Fatal("utun leaked")
		}
	}
}

//go:build windows

package winservice

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"testing"

	"bluegrass/native/core/serviceipc"
)

func TestUpdateResolverCannotMutateSessionOrResolveArbitraryHost(t *testing.T) {
	for _, raw := range []string{
		`{"id":"1","command":"ResolveUpdateHost","host":"updates.example.test"}`,
		`{"id":"1","command":"ResolveUpdateHost","host":"attacker.example.test"}`,
		`{"id":"1","command":"Connect","host":"updates.example.test"}`,
		`{"id":"1","command":"Release","host":"updates.example.test"}`,
		`{"id":"1","command":"ResolveUpdateHost","host":"updates.example.test","profile":{}}`,
	} {
		client, server := net.Pipe()
		called := false
		done := make(chan error, 1)
		go func() {
			defer server.Close()
			done <- serveUpdateResolver(server, []string{"updates.example.test"}, func(context.Context, string) ([]netip.Addr, error) {
				called = true
				return []netip.Addr{netip.MustParseAddr("8.8.4.4")}, nil
			})
		}()
		if err := serviceipc.WriteFrame(client, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		data, err := serviceipc.ReadFrame(client)
		if err != nil {
			t.Fatal(err)
		}
		client.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var reply updateResolveResponse
		if json.Unmarshal(data, &reply) != nil {
			t.Fatal("invalid response")
		}
		want := raw == `{"id":"1","command":"ResolveUpdateHost","host":"updates.example.test"}`
		if reply.OK != want || called != want || !want && len(reply.Addresses) != 0 {
			t.Fatal("read-only helper boundary bypassed", raw, string(data))
		}
	}
}

func TestUpdateResolverRejectsDifferentProtectionOwner(t *testing.T) {
	f := newCompositionFixture()
	if _, err := f.e.resolveUpdateHost(context.Background(), "updates.example.test", "S-1-5-21-9"); err == nil || len(f.log.snapshot()) != 0 {
		t.Fatal("other owner reached control DNS transaction")
	}
}

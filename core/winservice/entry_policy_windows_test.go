//go:build windows

package winservice

import (
	"context"
	"testing"

	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/winprotect"
)

func TestServiceUsesProtocolRequirementsForExactEntryAndSOCKSCallback(t *testing.T) {
	for _, tc := range []struct {
		config protocolconfig.Config
		want   winprotect.EntryPolicy
	}{
		{protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{}}, winprotect.EntryPolicy{TCP: true, AuthenticatedSOCKSRelay: true}},
		{protocolconfig.Config{Version: 1, Type: "shadowsocks", Shadowsocks: &protocolconfig.Shadowsocks{Method: "aes-128-gcm", Password: "fixture"}}, winprotect.EntryPolicy{TCP: true, UDP: true}},
		{protocolconfig.Config{Version: 1, Type: "hysteria2", TLS: &protocolconfig.TLS{ServerName: "entry.invalid"}, Hysteria2: &protocolconfig.Hysteria2{Password: "fixture"}}, winprotect.EntryPolicy{UDP: true}},
		{protocolconfig.Config{Version: 1, Type: "http", HTTP: &protocolconfig.HTTP{}}, winprotect.EntryPolicy{TCP: true}},
	} {
		t.Run(tc.config.Type, func(t *testing.T) {
			f := newCompositionFixture()
			p := compositionProfile()
			p.Protocol = tc.config.Type
			p.ProtocolConfig = &tc.config
			if err := f.e.Prepare(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if f.p.entryPolicy != tc.want {
				t.Fatal("service broadened protocol requirements", f.p.entryPolicy)
			}
			if (f.callbacks[0] != nil) != tc.want.AuthenticatedSOCKSRelay {
				t.Fatal("relay callback leaked to another protocol")
			}
			if (f.releaseCallbacks[0] != nil) != tc.want.AuthenticatedSOCKSRelay {
				t.Fatal("relay release callback was missing or leaked to another protocol")
			}
		})
	}
}

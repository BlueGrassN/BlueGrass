//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"bluegrass/native/core/tunengine"
)

type routingProtectionFixture struct {
	*protectionFixture
	grants, releases int
	removeErr        error
}

func (p *routingProtectionFixture) AllowDirect(network string, ep netip.AddrPort) error {
	p.grants++
	return nil
}
func (p *routingProtectionFixture) RemoveDirect(network string, ep netip.AddrPort) error {
	p.releases++
	return p.removeErr
}

func TestDirectCallbacksRequireCurrentSplitSessionAndOpenGate(t *testing.T) {
	for _, mode := range []string{"global", "bypass_cn"} {
		t.Run(mode, func(t *testing.T) {
			f := newCompositionFixture()
			p := &routingProtectionFixture{protectionFixture: f.p}
			f.e.protection = p
			var configs []tunengine.Config
			original := f.e.newTun
			f.e.newTun = func(c tunengine.Config) (ownedTunnel, error) { configs = append(configs, c); return original(c) }
			profile := compositionProfile()
			profile.RoutingMode = mode
			prepare := func() {
				t.Helper()
				if err := f.e.Prepare(context.Background(), profile); err != nil {
					t.Fatal(err)
				}
			}
			prepare()
			c := configs[0]
			if mode == "global" {
				if c.AllowDirect != nil || c.ReleaseDirect != nil {
					t.Fatal("global granted business direct capability")
				}
				return
			}
			if c.AllowDirect == nil || c.ReleaseDirect == nil {
				t.Fatal("split callbacks absent")
			}
			ep := netip.MustParseAddrPort("223.5.5.5:443")
			if c.AllowDirect("tcp", ep) == nil || p.grants != 0 {
				t.Fatal("direct allowed before business gate")
			}
			if err := f.e.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			if err := c.AllowDirect("tcp", ep); err != nil || p.grants != 1 {
				t.Fatal("live direct rejected", err)
			}
			if err := f.e.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			prepare()
			if err := f.e.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			if c.AllowDirect("tcp", ep) == nil {
				t.Fatal("old callback authorized new session")
			}
			if err := c.ReleaseDirect("tcp", ep); err != nil || p.releases != 0 {
				t.Fatal("old callback revoked new session")
			}
			current := configs[len(configs)-1]
			if err := current.AllowDirect("tcp", ep); err != nil {
				t.Fatal(err)
			}
			p.removeErr = errors.New("fixture-removal-failed")
			before := f.log.count("block")
			if err := current.ReleaseDirect("tcp", ep); !errors.Is(err, p.removeErr) {
				t.Fatal("removal failure hidden", err)
			}
			if f.log.count("block") != before+1 || current.AllowDirect("tcp", ep) == nil {
				t.Fatal("failed revocation left authorization enabled")
			}
		})
	}
}

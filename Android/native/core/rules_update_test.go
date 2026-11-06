package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"bluegrass/native/core/routingpolicy"
	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

type updateResolverFixture struct {
	calls int
	fail  bool
}

type rulesRoundTripper func(*http.Request) (*http.Response, error)

func (f rulesRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewDNSRoutersUseUpdatedRulesWhileExistingSessionKeepsItsSnapshot(t *testing.T) {
	ruleManager.Lock()
	old, oldPath := ruleManager.store, ruleManager.path
	ruleManager.store, ruleManager.path = nil, ""
	ruleManager.Unlock()
	t.Cleanup(func() { ruleManager.Lock(); ruleManager.store, ruleManager.path = old, oldPath; ruleManager.Unlock() })
	if err := ConfigureRoutingRules(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	update := func(tag, domestic string) {
		site, _ := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{
			{Code: "CN", Domain: []*geodata.Domain{{Type: geodata.Domain_Domain, Value: domestic}}},
			{Code: "GEOLOCATION-!CN", Domain: []*geodata.Domain{{Type: geodata.Domain_Domain, Value: "foreign.invalid"}}},
		}})
		ip, _ := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "CN", Cidr: []*geodata.CIDR{{Ip: []byte{223, 5, 5, 0}, Prefix: 24}}}}})
		meta, _ := json.Marshal(map[string]any{"tag_name": tag, "assets": []map[string]any{
			{"name": "geosite.dat", "size": len(site), "state": "uploaded", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256(site))},
			{"name": "geoip.dat", "size": len(ip), "state": "uploaded", "digest": fmt.Sprintf("sha256:%x", sha256.Sum256(ip))},
		}})
		client := &http.Client{Transport: rulesRoundTripper(func(req *http.Request) (*http.Response, error) {
			body := meta
			if strings.HasSuffix(req.URL.Path, "/geosite.dat") {
				body = site
			}
			if strings.HasSuffix(req.URL.Path, "/geoip.dat") {
				body = ip
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
		})}
		if state := configuredRules().Update(context.Background(), client, true); state.LastError != "" {
			t.Fatal(state.LastError)
		}
	}
	update("203001010000", "old-routing.invalid")
	first, _, _, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	update("203001020000", "new-routing.invalid")
	second, _, _, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	if first.resolver.rules.Domain("old-routing.invalid") != routingpolicy.Direct || first.resolver.rules.Domain("new-routing.invalid") != routingpolicy.Unknown {
		t.Fatal("active session changed")
	}
	if second.resolver.rules.Domain("new-routing.invalid") != routingpolicy.Direct || second.resolver.rules.Domain("old-routing.invalid") != routingpolicy.Unknown {
		t.Fatal("new connection did not load updated rules")
	}
}

func (r *updateResolverFixture) LookupPublic(_ context.Context, host string) ([]netip.Addr, error) {
	r.calls++
	if r.fail {
		return nil, errDNS
	}
	return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("8.8.8.8")}, nil
}

func TestRulesUpdateProxyDialerRestrictsHostsAndPreservesSelectedPath(t *testing.T) {
	r := new(updateResolverFixture)
	out := new(probeOutbound)
	valid := true
	dial := routingUpdateDialer(r, out, func() bool { return valid })
	for _, address := range []string{"evil.invalid:443", "github.com:80", "github.com.evil.invalid:443", "127.0.0.1:443"} {
		if c, e := dial(context.Background(), "tcp", address); e == nil || c != nil {
			t.Fatal("unsafe updater target")
		}
	}
	if r.calls != 0 || out.called {
		t.Fatal("unsafe target reached DNS/network")
	}
	c, e := dial(context.Background(), "tcp", "api.github.com:443")
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	if r.calls != 1 || out.dest != "8.8.8.8:443" {
		t.Fatal("update bypassed selected public IPv4 path")
	}
	r.fail = true
	out.called = false
	if _, e = dial(context.Background(), "tcp", "github.com:443"); e == nil || out.called {
		t.Fatal("DNS failure fell back direct")
	}
	valid = false
	r.fail = false
	before := r.calls
	if _, e = dial(context.Background(), "tcp", "github.com:443"); e == nil || r.calls != before {
		t.Fatal("closed session used")
	}
}

func TestRuleStatusIsAvailableWithoutNetworkAndPathIsProcessScoped(t *testing.T) {
	ruleManager.Lock()
	old, oldPath := ruleManager.store, ruleManager.path
	ruleManager.store = nil
	ruleManager.path = ""
	ruleManager.Unlock()
	t.Cleanup(func() { ruleManager.Lock(); ruleManager.store = old; ruleManager.path = oldPath; ruleManager.Unlock() })
	var state map[string]any
	if json.Unmarshal([]byte(RoutingRulesStatus()), &state) != nil || state["source"] != "bundled" {
		t.Fatal("no offline status")
	}
	dir := t.TempDir()
	if ConfigureRoutingRules(dir) != nil || ConfigureRoutingRules(dir) != nil {
		t.Fatal("idempotent initialization failed")
	}
	if ConfigureRoutingRules(t.TempDir()) == nil {
		t.Fatal("process cache path changed")
	}
	if json.Unmarshal([]byte(RoutingRulesStatus()), &state) != nil || state["updating"] != false {
		t.Fatal("status triggered download")
	}
}

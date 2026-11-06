package localengine

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	"bluegrass/native/core/routingpolicy"
	"github.com/miekg/dns"
)

// Exercise real framed DNS rather than replacing the classifier or resolver.
func TestBusinessRoutesResolveOnChosenPathAndDialSameAddress(t *testing.T) {
	rules, err := routingpolicy.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, host, answer string
		mode               routingpolicy.Mode
		want               []string
	}{
		{"domestic-domain-foreign-cdn", "www.baidu.com", "8.8.8.8", routingpolicy.BypassCN, []string{"direct:223.5.5.5:53", "direct:8.8.8.8:443"}},
		{"foreign-domain-cn-address", "www.google.com", "223.5.5.5", routingpolicy.BypassCN, []string{"proxy:1.1.1.1:53", "proxy:223.5.5.5:443"}},
		{"unknown-domain-cn-address", "unlisted.routing-test.invalid", "223.5.5.5", routingpolicy.BypassCN, []string{"proxy:1.1.1.1:53", "direct:223.5.5.5:443"}},
		{"global-keeps-selected-resolution", "www.baidu.com", "", routingpolicy.Global, []string{"proxy:www.baidu.com:443"}},
		{"cn-literal", "223.5.5.5", "", routingpolicy.BypassCN, []string{"direct:223.5.5.5:443"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			dial := func(path string) dialFunc {
				return func(ctx context.Context, network, address string) (net.Conn, error) {
					calls = append(calls, path+":"+address)
					c, s := net.Pipe()
					if _, port, _ := net.SplitHostPort(address); port == "53" {
						go func() {
							defer s.Close()
							wire := &dns.Conn{Conn: s}
							q, e := wire.ReadMsg()
							if e != nil {
								return
							}
							r := new(dns.Msg)
							r.SetReply(q)
							r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(tc.answer)}}
							_ = wire.WriteMsg(r)
						}()
					} else {
						_ = s.Close()
					}
					return c, nil
				}
			}
			conn, err := businessRouter(tc.mode, rules, dial("proxy"), dial("direct"))(context.Background(), "tcp", net.JoinHostPort(tc.host, "443"))
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			if !slices.Equal(calls, tc.want) {
				t.Fatalf("paths %v, want %v", calls, tc.want)
			}
		})
	}
}

func TestBusinessRoutingDNSFailureCannotFallBackDirect(t *testing.T) {
	rules, err := routingpolicy.Load()
	if err != nil {
		t.Fatal(err)
	}
	selected := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("fixture") }
	direct := func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("failed foreign DNS escaped directly")
		return nil, nil
	}
	if c, err := businessRouter(routingpolicy.BypassCN, rules, selected, direct)(context.Background(), "tcp", "www.google.com:443"); err == nil || c != nil {
		t.Fatal("resolution failure accepted")
	}
}

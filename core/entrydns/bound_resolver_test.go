package entrydns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type memoryDatagram struct{ net.Conn }

func (c memoryDatagram) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Read(p)
	return n, c.RemoteAddr(), err
}
func (c memoryDatagram) WriteTo(p []byte, _ net.Addr) (int, error) { return c.Write(p) }

func TestResolverUsesSuppliedBoundSocketForUDPAndTCP(t *testing.T) {
	for _, transport := range []string{"udp", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			var connection *localTestConn
			done := make(chan error, 1)
			dial := func(ctx context.Context, network, address string) (net.Conn, error) {
				if network != transport || address != "1.1.1.1:53" {
					t.Fatal("resolver escaped the exact selected transport/endpoint", network, address)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("bound dial lost total DNS deadline")
				}
				client, server := net.Pipe()
				connection = &localTestConn{Conn: client}
				var a net.Conn = connection
				var b net.Conn = server
				if transport == "udp" {
					a, b = memoryDatagram{a}, memoryDatagram{b}
				}
				go func() {
					defer server.Close()
					wire := &dns.Conn{Conn: b}
					query, err := wire.ReadMsg()
					if err != nil {
						done <- err
						return
					}
					reply := new(dns.Msg)
					reply.SetReply(query)
					reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("8.8.4.4")}}
					done <- wire.WriteMsg(reply)
				}()
				return a, nil
			}
			p := Policy{Mode: "custom", Candidates: []Candidate{{Address: "1.1.1.1", Transport: transport}}}
			ip, err := resolveWithDial(context.Background(), "api.example", p, nil, dial)
			if err != nil || ip.String() != "8.8.4.4" || connection == nil || !connection.closed {
				t.Fatal("bound DNS exchange failed or leaked socket", ip, err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundSocketFailureNeverUsesDefaultDialerOrCustomFallback(t *testing.T) {
	p := Policy{Mode: "custom", Candidates: []Candidate{{Address: "192.168.1.1", Transport: "udp"}, {Address: "192.168.1.1", Transport: "tcp"}}}
	var networks []string
	_, err := resolveWithDial(context.Background(), "api.example", p, []netip.AddrPort{netip.MustParseAddrPort("8.8.8.8:53")}, func(_ context.Context, network, address string) (net.Conn, error) {
		if address != "192.168.1.1:53" {
			t.Fatal("custom binding failure escaped selected DNS")
		}
		networks = append(networks, network)
		return nil, errors.New("physical binding failed")
	})
	if err == nil || len(networks) != 2 || networks[0] != "udp" || networks[1] != "tcp" {
		t.Fatal("failed binding escaped bounded policy", err, networks)
	}
}

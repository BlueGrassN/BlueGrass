package tunengine

import (
	"context"
	"errors"
	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"net"
	"strings"
	"time"
)

// A single trusted physical link, no system resolver or cross-interface fanout.
type mdnsTransport struct {
	ctx    context.Context
	cancel context.CancelFunc
	iface  string
}

func newMDNSTransport(ctx context.Context, iface string) *mdnsTransport {
	ctx, cancel := context.WithCancel(ctx)
	return &mdnsTransport{ctx: ctx, cancel: cancel, iface: iface}
}
func (t *mdnsTransport) Start() error { return t.ctx.Err() }
func (t *mdnsTransport) Close() error { t.cancel(); return nil }
func (*mdnsTransport) Reset()         {}
func (t *mdnsTransport) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	if len(m.Question) != 1 || !isMDNSName(m.Question[0].Name) {
		return nil, errors.New("INVALID_MDNS_QUERY")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	if ctx.Err() != nil || t.ctx.Err() != nil {
		return nil, errGateClosed
	}
	iface, e := net.InterfaceByName(t.iface)
	if e != nil {
		return nil, e
	}
	v6 := strings.HasSuffix(normalizedName(m.Question[0].Name), "ip6.arpa")
	network, group := "udp4", &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	if v6 {
		network = "udp6"
		group = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353, Zone: t.iface}
	}
	conn, e := net.ListenMulticastUDP(network, iface, group)
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer closeOnCancel()
	if v6 {
		pc := ipv6.NewPacketConn(conn)
		if e = pc.SetMulticastInterface(iface); e == nil {
			e = pc.SetMulticastHopLimit(255)
		}
	} else {
		pc := ipv4.NewPacketConn(conn)
		if e = pc.SetMulticastInterface(iface); e == nil {
			e = pc.SetMulticastTTL(255)
		}
	}
	if e != nil {
		return nil, e
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	query := new(dns.Msg)
	query.Question = append([]dns.Question(nil), m.Question...)
	wire, e := query.Pack()
	if e != nil {
		return nil, e
	}
	if _, e = conn.WriteToUDP(wire, group); e != nil {
		return nil, e
	}
	packet := make([]byte, 65535)
	for {
		n, source, e := conn.ReadFromUDP(packet)
		if e != nil {
			return nil, e
		}
		if source.Port != 5353 {
			continue
		}
		response := new(dns.Msg)
		if response.Unpack(packet[:n]) != nil || !response.Response || response.Opcode != dns.OpcodeQuery {
			continue
		}
		match := false
		for _, rr := range response.Answer {
			if strings.EqualFold(rr.Header().Name, m.Question[0].Name) && (rr.Header().Rrtype == m.Question[0].Qtype || rr.Header().Rrtype == dns.TypeCNAME) {
				match = true
			}
		}
		if !match {
			continue
		}
		response.Id = m.Id
		response.Question = append([]dns.Question(nil), m.Question...)
		return response, nil
	}
}

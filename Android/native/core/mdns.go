package bridge

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"

	mdns "github.com/miekg/dns"
)

const (
	androidMDNSTimeout   = 1500 * time.Millisecond
	androidMDNSMaxPacket = 9000
	androidMDNSMaxReads  = 64
	androidMDNSQU        = 1 << 15
)

var (
	errMDNSUnavailable = errors.New("MDNS_UNAVAILABLE")
	errMDNSQuestion    = errors.New("MDNS_INVALID_QUESTION")
)

type androidMDNSTransport struct {
	platform     *platformBridge
	parent       context.Context
	mu           sync.Mutex
	lifetime     context.Context
	cancel       context.CancelFunc
	closed       bool
	slots        chan struct{}
	listenPacket func(context.Context, *net.ListenConfig, string, string) (net.PacketConn, error)
}

func newAndroidMDNS(ctx context.Context, p *platformBridge) (*androidMDNSTransport, error) {
	if p == nil || p.callback == nil {
		return nil, errMDNSUnavailable
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &androidMDNSTransport{platform: p, parent: ctx, lifetime: lifetime, cancel: cancel,
		slots: make(chan struct{}, 16), listenPacket: func(ctx context.Context, lc *net.ListenConfig, network, address string) (net.PacketConn, error) {
			return lc.ListenPacket(ctx, network, address)
		}}, nil
}

func (t *androidMDNSTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.cancel()
	return nil
}

func (t *androidMDNSTransport) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancel()
	if !t.closed {
		t.lifetime, t.cancel = context.WithCancel(t.parent)
	}
}

func (t *androidMDNSTransport) ExchangeAsync(ctx context.Context, message *mdns.Msg, callback func(*mdns.Msg, error)) {
	go func() { callback(t.Exchange(ctx, message)) }()
}

func (t *androidMDNSTransport) Exchange(ctx context.Context, message *mdns.Msg) (*mdns.Msg, error) {
	if message == nil || message.Response || message.Opcode != mdns.OpcodeQuery || len(message.Question) != 1 {
		return nil, errMDNSQuestion
	}
	question := message.Question[0]
	question.Name = mdns.CanonicalName(question.Name)
	question.Qclass &^= androidMDNSQU
	_, validName := mdns.IsDomainName(question.Name)
	if !validName || !androidMDNSLocalName(question.Name) || question.Qclass != mdns.ClassINET || question.Qtype == 0 || question.Qtype == mdns.TypeAXFR || question.Qtype == mdns.TypeIXFR || question.Qtype == mdns.TypeOPT {
		return nil, errMDNSQuestion
	}
	t.mu.Lock()
	lifetime, closed := t.lifetime, t.closed
	t.mu.Unlock()
	if closed || lifetime.Err() != nil || ctx.Err() != nil || !t.platform.valid() {
		return nil, errMDNSUnavailable
	}
	prefix, ok := mdnsPhysicalIPv4(t.platform.info)
	if !ok {
		return nil, errMDNSUnavailable
	}
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	default:
		return nil, errMDNSUnavailable
	}
	queryCtx, cancel := context.WithTimeout(ctx, androidMDNSTimeout)
	defer cancel()
	stopLifetime := context.AfterFunc(lifetime, cancel)
	defer stopLifetime()
	validNetwork := func() bool {
		return queryCtx.Err() == nil && lifetime.Err() == nil && t.platform.valid()
	}
	request := &mdns.Msg{MsgHdr: mdns.MsgHdr{Id: message.Id}, Question: []mdns.Question{question}}
	request.Question[0].Qclass |= androidMDNSQU
	wire, err := request.Pack()
	if err != nil {
		return nil, errMDNSQuestion
	}
	lc := &net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		if !validNetwork() {
			return errMDNSUnavailable
		}
		protected := false
		err := raw.Control(func(fd uintptr) {
			// Protect also binds the fd to Android's captured Network. No
			// SO_BINDTODEVICE, interface discovery, netlink, or multicast join.
			if validNetwork() && t.platform.AutoDetectInterfaceControl(int(fd)) == nil {
				protected = true
			}
		})
		if err != nil || !protected || !validNetwork() {
			return errMDNSUnavailable
		}
		return nil
	}}
	conn, err := t.listenPacket(queryCtx, lc, "udp4", net.JoinHostPort(prefix.Addr().String(), "0"))
	if err != nil {
		return nil, errMDNSUnavailable
	}
	var closeOnce sync.Once
	closeSocket := func() { closeOnce.Do(func() { _ = conn.Close() }) }
	defer closeSocket()
	stopSocket := context.AfterFunc(queryCtx, closeSocket)
	defer stopSocket()
	deadline, _ := queryCtx.Deadline()
	if conn.SetDeadline(deadline) != nil || !validNetwork() {
		return nil, errMDNSUnavailable
	}
	n, err := conn.WriteTo(wire, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	if err != nil || n != len(wire) {
		return nil, errMDNSUnavailable
	}
	// One byte beyond the cap detects truncation of overlarge datagrams.
	buffer := make([]byte, androidMDNSMaxPacket+1)
	for i := 0; i < androidMDNSMaxReads && validNetwork(); i++ {
		n, source, readErr := conn.ReadFrom(buffer)
		if readErr != nil {
			return nil, errMDNSUnavailable
		}
		if n < 12 || n > androidMDNSMaxPacket || !androidMDNSSource(source, prefix) {
			continue
		}
		var response mdns.Msg
		if response.Unpack(buffer[:n]) != nil {
			continue
		}
		answer := androidMDNSAnswer(&response, message, question)
		if answer != nil && validNetwork() {
			return answer, nil
		}
	}
	return nil, errMDNSUnavailable
}

func androidMDNSLocalName(name string) bool {
	return mdns.IsSubDomain("local.", name)
}

func androidMDNSSource(source net.Addr, prefix netip.Prefix) bool {
	udp, ok := source.(*net.UDPAddr)
	if !ok || udp == nil || udp.Port != 5353 || udp.Zone != "" {
		return false
	}
	address, ok := netip.AddrFromSlice(udp.IP)
	address = address.Unmap()
	return ok && address.Is4() && dnsPhysicalUnicast(address) && dnsPrefixHost(prefix, address)
}

func androidMDNSAnswer(candidate, request *mdns.Msg, question mdns.Question) *mdns.Msg {
	if !candidate.Response || candidate.Opcode != mdns.OpcodeQuery || candidate.Rcode != mdns.RcodeSuccess || candidate.Truncated || (candidate.Id != 0 && candidate.Id != request.Id) || len(candidate.Question) > 1 {
		return nil
	}
	if len(candidate.Question) == 1 {
		q := candidate.Question[0]
		if !strings.EqualFold(q.Name, question.Name) || q.Qtype != question.Qtype || q.Qclass&^androidMDNSQU != question.Qclass {
			return nil
		}
	}
	answer := new(mdns.Msg).SetReply(request)
	answer.Authoritative = candidate.Authoritative
	answer.Question[0].Qclass &^= androidMDNSQU
	// Accept only direct answers to the bounded question. Unrelated records and
	// unsolicited authority/additional data must not enter the caller's cache.
	// PTR/SRV targets can be looked up separately through the DNS policy.
	for _, record := range candidate.Answer {
		h := record.Header()
		if !strings.EqualFold(h.Name, question.Name) || h.Class&^androidMDNSQU != mdns.ClassINET || (h.Rrtype != question.Qtype && h.Rrtype != mdns.TypeCNAME && question.Qtype != mdns.TypeANY) {
			continue
		}
		copy := mdns.Copy(record)
		copy.Header().Class &^= androidMDNSQU
		answer.Answer = append(answer.Answer, copy)
	}
	if len(answer.Answer) == 0 {
		return nil
	}
	return answer
}

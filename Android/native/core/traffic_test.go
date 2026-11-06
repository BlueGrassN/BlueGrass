package bridge

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"encoding/json"
	M "github.com/sagernet/sing/common/metadata"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func trafficValues(t *testing.T, s *Session) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s.Traffic()), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func echoPayload(t *testing.T, c net.Conn, payload string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(time.Second))
	done := make(chan error, 1)
	go func() { _, err := io.WriteString(c, payload); done <- err }()
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(c, received); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if string(received) != payload {
		t.Fatal("payload changed")
	}
	c.Close()
}
func TestTrafficCountsBusinessTCPAndUDPButNotControlTransport(t *testing.T) {
	s, _, _, _, _, _ := routingSession(t, routingpolicy.Global)
	for _, network := range []string{"tcp", "udp"} {
		c, err := s.dial(context.Background(), network, M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 443))
		if err != nil {
			t.Fatal(err)
		}
		echoPayload(t, c, "payload")
	}
	// Probe, DNS and rule updater use the raw selected transport, not s.dial.
	control, err := s.selected.DialContext(context.Background(), "tcp", "8.8.4.4:443")
	if err != nil {
		t.Fatal(err)
	}
	echoPayload(t, control, "excluded-control")
	v := trafficValues(t, s)
	if v["uploadBytes"] != "14" || v["downloadBytes"] != "14" || v["complete"] != true {
		t.Fatal(v)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := trafficValues(t, s); after["uploadBytes"] != "14" {
		t.Fatal(after)
	}
}

type partialTrafficConn struct{ net.Conn }

func (partialTrafficConn) Read(p []byte) (int, error) { copy(p, "ok"); return 2, io.EOF }
func (partialTrafficConn) Write([]byte) (int, error)  { return 3, io.ErrShortWrite }
func TestTrafficAccountsPartialSuccessAndSaturates(t *testing.T) {
	s := &Session{}
	c := &trafficConn{Conn: partialTrafficConn{}, counters: &s.traffic}
	c.Read(make([]byte, 8))
	c.Write([]byte("partial"))
	if v := trafficValues(t, s); v["uploadBytes"] != "3" || v["downloadBytes"] != "2" {
		t.Fatal(v)
	}
	s.traffic.upload.Store(^uint64(0) - 1)
	c.Write([]byte("partial"))
	if v := trafficValues(t, s); v["uploadBytes"] != "18446744073709551615" || v["complete"] != false {
		t.Fatal(v)
	}
}
func TestTrafficCloseUnblocksReadAndFreezesCounters(t *testing.T) {
	s, _, _, _, _, _ := routingSession(t, routingpolicy.Global)
	c, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 443))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { c.Read(make([]byte, 10)); close(done) }()
	stopped := make(chan error, 1)
	go func() { stopped <- s.Close() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close deadlocked")
	}
	<-done
	if n, _ := c.Write([]byte("late")); n != 0 {
		t.Fatal("write after close")
	}
	if v := trafficValues(t, s); v["uploadBytes"] != "0" || v["downloadBytes"] != "0" {
		t.Fatal(v)
	}
}

func TestTrafficExcludesProtectedDirectLANPayload(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var local netip.Addr
	for _, address := range addresses {
		prefix, e := netip.ParsePrefix(address.String())
		if e == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() {
			local = prefix.Addr()
			break
		}
	}
	if !local.IsValid() {
		t.Skip("no local IPv4 LAN interface")
	}
	s, _, _, selected, _, _ := routingSession(t, routingpolicy.Global)
	protected := &allowingRoutingPlatform{}
	s.platform.callback = protected
	s.platform.info.Addresses = []string{local.String() + "/32"}
	tcp, err := net.Listen("tcp4", net.JoinHostPort(local.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := tcp.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		io.Copy(c, c)
	}()
	c, err := s.dial(context.Background(), "tcp", M.ParseSocksaddr(tcp.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	echoPayload(t, c, "local-tcp")
	<-done
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(local.String())})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(2 * time.Second))
	udpDone := make(chan struct{})
	go func() {
		defer close(udpDone)
		b := make([]byte, 100)
		n, peer, e := udp.ReadFromUDP(b)
		if e == nil {
			udp.WriteToUDP(b[:n], peer)
		}
	}()
	c, err = s.dial(context.Background(), "udp", M.ParseSocksaddr(udp.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	echoPayload(t, c, "local-udp")
	<-udpDone
	if selected.count() != 0 || protected.calls.Load() != 2 {
		t.Fatal("LAN did not use protected direct path")
	}
	if v := trafficValues(t, s); v["uploadBytes"] != "0" || v["downloadBytes"] != "0" {
		t.Fatal("direct payload counted", v)
	}
}

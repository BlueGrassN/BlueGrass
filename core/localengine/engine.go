// Package localengine embeds Xray's typed outbounds behind a closed-by-default
// loopback HTTP gateway. It never writes Windows proxy, firewall or route settings.
package localengine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"bluegrass/native/core/tunengine"
)

var errClosed = errors.New("BUSINESS_GATE_CLOSED")

type Local struct {
	mu               sync.Mutex
	observed         string
	gate             bool
	generation       uint64
	dial             func(context.Context, string, string) (net.Conn, error)
	selectedDial     func(context.Context, string, string) (net.Conn, error)
	session          context.Context
	cancel           context.CancelFunc
	conns            map[net.Conn]struct{}
	listener         net.Listener
	server           *http.Server
	upload, download atomic.Uint64
	entryDNS         entrydns.Policy
	native           io.Closer
	cleanupErr       error
	closed           bool
}

func New(port uint16) (*Local, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		return nil, errors.New("LOCAL_LISTENER_FAILED")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Local{listener: listener, session: ctx, cancel: cancel, conns: make(map[net.Conn]struct{})}
	e.server = &http.Server{Handler: http.HandlerFunc(e.serve), ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 15 * time.Second, MaxHeaderBytes: 32 * 1024,
		ConnState: func(conn net.Conn, state http.ConnState) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if state == http.StateClosed || state == http.StateHijacked {
				delete(e.conns, conn)
			} else {
				e.conns[conn] = struct{}{}
			}
		}}
	go func() { _ = e.server.Serve(listener) }()
	return e, nil
}
func (e *Local) Address() string { return e.listener.Addr().String() }

// VerifiedIPv4 returns the current session's last observed public IPv4.
// Empty means not yet observed, never ineligible.
func (e *Local) VerifiedIPv4() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.gate || e.session == nil || e.session.Err() != nil {
		return ""
	}
	return e.observed
}
func (e *Local) Counters() (uint64, uint64) { return e.upload.Load(), e.download.Load() }

// Policy changes affect the next Prepare; existing authenticated sockets stay
// untouched. The copy prevents a caller from mutating an in-flight policy.
func (e *Local) SetEntryDNS(policy entrydns.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	policy.Candidates = append([]entrydns.Candidate(nil), policy.Candidates...)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.entryDNS = policy
	return nil
}

func (e *Local) Prepare(ctx context.Context, p engine.Profile) error {
	config, err := p.EffectiveProtocol()
	return e.prepare(ctx, p, config, err)
}

// PrepareProtocol follows the same shared profile/duplicate-auth validation as
// Prepare, including retirement of an old session when validation fails.
func (e *Local) PrepareProtocol(ctx context.Context, p engine.Profile, protocol protocolconfig.Config) error {
	p.ProtocolConfig = &protocol
	return e.Prepare(ctx, p)
}

func (e *Local) prepare(ctx context.Context, p engine.Profile, protocol protocolconfig.Config, configErr error) error {
	// Withdraw the previous outbound and observation before preparing a new
	// session. Late detector results cannot update the replacement.
	e.mu.Lock()
	connections := e.blockLocked()
	epoch, session, policy, closed := e.generation, e.session, e.entryDNS, e.closed
	e.mu.Unlock()
	if err := e.closeRetired(connections); err != nil {
		return err
	}
	if closed {
		return errors.New("ENGINE_CLOSED")
	}
	if configErr != nil {
		return configErr
	}
	routingMode, err := routingpolicy.Normalize(p.RoutingMode)
	if err != nil {
		return err
	}
	var rules *routingpolicy.Rules
	if routingMode == routingpolicy.BypassCN {
		rules, err = routingpolicy.Load()
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	unhook := context.AfterFunc(session, cancel)
	defer cancel()
	defer unhook()
	if ctx.Err() != nil || session.Err() != nil {
		return errors.New("STALE_PREPARE")
	}
	if p.Protocol != protocol.Type || p.Mode != "system_proxy" || protocol.Validate() != nil {
		return errors.New("UNSUPPORTED_PROFILE")
	}
	if err := protocol.Supported(); err != nil {
		return err
	}
	if p.Entry.Port < 1 || p.Entry.Port > 65535 {
		return errors.New("INVALID_ENTRY")
	}
	entry, err := netip.ParseAddr(p.Entry.Host)
	if err != nil {
		var candidates []netip.AddrPort
		if policy.Mode != "custom" {
			network, snapshotErr := tunengine.SystemNetwork(ctx)
			if snapshotErr != nil {
				return errors.New("ENTRY_DNS_FAILED")
			}
			candidates = network.EntryDNS
		}
		var lookupErr error
		entry, lookupErr = entrydns.Resolve(ctx, p.Entry.Host, policy, candidates)
		if lookupErr != nil {
			return errors.New("ENTRY_DNS_FAILED")
		}
	}
	if !entry.Is4() || !gate.PublicIPv4(entry.String()) {
		return errors.New("INVALID_ENTRY")
	}
	out, err := protocolconfig.NewOutbound(session, protocol, netip.AddrPortFrom(entry, uint16(p.Entry.Port)))
	if err != nil {
		return errors.New("ENGINE_PREPARE_FAILED")
	}
	e.mu.Lock()
	if e.closed || epoch != e.generation || session.Err() != nil || ctx.Err() != nil {
		e.mu.Unlock()
		_ = e.closeRetired(retiredLocal{native: out})
		return errors.New("STALE_PREPARE")
	}
	e.native = out
	e.observed = ""
	e.dial = out.DialContext
	e.selectedDial = out.DialContext
	e.dial = businessRouter(routingMode, rules, out.DialContext, (&net.Dialer{Timeout: 10 * time.Second}).DialContext)
	e.upload.Store(0)
	e.download.Store(0)
	e.mu.Unlock()
	return nil
}

func (e *Local) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	e.mu.Lock()
	dial, epoch, session := e.dial, e.generation, e.session
	if e.selectedDial != nil {
		dial = e.selectedDial
	}
	e.mu.Unlock()
	if dial == nil || session.Err() != nil {
		return engine.ProbeResult{}, errors.New("ENGINE_NOT_PREPARED")
	}
	probeCtx, cancel := context.WithCancel(ctx)
	unhook := context.AfterFunc(session, cancel)
	defer cancel()
	defer unhook()
	address, err := exitprobe.Probe(probeCtx, dial, exitprobe.Options{})
	if err != nil {
		return engine.ProbeResult{}, err
	}
	return e.acceptProbe(probeCtx, epoch, session, address)
}

func (e *Local) acceptProbe(ctx context.Context, epoch uint64, session context.Context, address string) (engine.ProbeResult, error) {
	e.mu.Lock()
	if ctx.Err() != nil || session.Err() != nil || epoch != e.generation {
		e.mu.Unlock()
		return engine.ProbeResult{}, errors.New("STALE_PROBE")
	}
	if !exitprobe.ValidObservedAddress(address) {
		e.mu.Unlock()
		return engine.ProbeResult{}, errors.New("INVALID_EXIT_RESPONSE")
	}
	e.observed = address
	e.mu.Unlock()
	return engine.ProbeResult{IPv4: address, Authenticated: true, Reachable: true}, nil
}

func (e *Local) SetBusinessGate(open bool) error {
	e.mu.Lock()
	if open {
		defer e.mu.Unlock()
		if e.closed || e.cleanupErr != nil || e.dial == nil || e.session == nil || e.session.Err() != nil {
			return errors.New("ENGINE_NOT_PREPARED")
		}
		e.gate = true
		return nil
	}
	connections := e.blockLocked()
	e.mu.Unlock()
	return e.closeRetired(connections)
}

// Caller holds mu. Gate closure retires the session and its observation.
// Reopening requires a fresh Prepare, but never a successful detector result.
func (e *Local) blockLocked() retiredLocal {
	e.gate, e.observed = false, ""
	e.generation++
	e.cancel()
	e.session, e.cancel = context.WithCancel(context.Background())
	connections := retiredLocal{conns: e.conns, native: e.native}
	e.native, e.dial, e.selectedDial = nil, nil, nil
	e.conns = make(map[net.Conn]struct{})
	return connections
}

type retiredLocal struct {
	conns  map[net.Conn]struct{}
	native io.Closer
}

func (e *Local) closeRetired(retired retiredLocal) error {
	for conn := range retired.conns {
		_ = conn.Close()
	}
	err := protocolconfig.CloseOutbound(retired.native)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.cleanupErr = errors.New("ENGINE_STOP_FAILED")
	}
	return e.cleanupErr
}
func (e *Local) Stop(context.Context) error {
	return e.SetBusinessGate(false)
}
func (e *Local) Close() error {
	e.mu.Lock()
	e.closed = true
	retired := e.blockLocked()
	e.cancel()
	e.mu.Unlock()
	err := e.closeRetired(retired)
	serverErr := e.server.Close()
	// Close also owns the listener when the Serve goroutine has not yet
	// registered it with http.Server (for example an immediate parent EOF).
	listenerErr := e.listener.Close()
	if errors.Is(listenerErr, net.ErrClosed) {
		listenerErr = nil
	}
	return errors.Join(err, serverErr, listenerErr)
}

func (e *Local) businessDial(ctx context.Context, network, address string) (net.Conn, error) {
	if !validTarget(address) {
		return nil, errors.New("INVALID_TARGET")
	}
	e.mu.Lock()
	if !e.gate || e.dial == nil {
		e.mu.Unlock()
		return nil, errClosed
	}
	epoch, dial, session := e.generation, e.dial, e.session
	e.mu.Unlock()
	dialCtx, cancel := context.WithCancel(ctx)
	unhook := context.AfterFunc(session, cancel)
	connection, err := dial(dialCtx, network, address)
	if err != nil {
		unhook()
		cancel()
		return nil, errors.New("UPSTREAM_CONNECT_FAILED")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.gate || epoch != e.generation || dialCtx.Err() != nil {
		_ = connection.Close()
		unhook()
		cancel()
		return nil, errClosed
	}
	e.conns[connection] = struct{}{}
	return &countedConn{Conn: connection, engine: e, generation: epoch, cancel: cancel, unhook: unhook}, nil
}

type countedConn struct {
	net.Conn
	engine     *Local
	generation uint64
	cancel     context.CancelFunc
	unhook     func() bool
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.engine.mu.Lock()
	if c.generation == c.engine.generation {
		c.engine.download.Add(uint64(n))
	}
	c.engine.mu.Unlock()
	return n, err
}
func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.engine.mu.Lock()
	if c.generation == c.engine.generation {
		c.engine.upload.Add(uint64(n))
	}
	c.engine.mu.Unlock()
	return n, err
}
func (c *countedConn) Close() error {
	if c.unhook != nil {
		c.unhook()
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.engine.mu.Lock()
	delete(c.engine.conns, c.Conn)
	c.engine.mu.Unlock()
	return c.Conn.Close()
}
func (c *countedConn) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return c.Conn.Close()
}
func validTarget(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || len(host) > 253 {
		return false
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return gate.PublicIPv4(ip.Unmap().String())
	}
	return !strings.ContainsAny(host, " /\\\x00\r\n\t")
}
func stripHop(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}
func (e *Local) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	open := e.gate
	e.mu.Unlock()
	if !open {
		http.Error(w, "Proxy paused", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodConnect {
		e.connect(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "HTTP proxy request required", http.StatusBadRequest)
		return
	}
	clone := r.Clone(r.Context())
	clone.RequestURI = ""
	clone.Close = true
	stripHop(clone.Header)
	transport := &http.Transport{Proxy: nil, DialContext: e.businessDial, DisableKeepAlives: true, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 64 * 1024}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(clone)
	if err != nil {
		http.Error(w, "Upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	stripHop(response.Header)
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Connection", "close")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
func (e *Local) connect(w http.ResponseWriter, r *http.Request) {
	// After Hijack, a client upload EOF cancels the HTTP request context, but
	// the tunnel still owns its downlink. Keep that flow until close or pause.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	unhook := context.AfterFunc(r.Context(), cancel)
	defer unhook()
	upstream, err := e.businessDial(ctx, "tcp", r.Host)
	if err != nil {
		http.Error(w, "Upstream unavailable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Unsupported connection", 500)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		return
	}
	unhook()
	defer client.Close()
	e.mu.Lock()
	if !e.gate || ctx.Err() != nil || upstream.(*countedConn).generation != e.generation {
		e.mu.Unlock()
		return
	}
	e.conns[client] = struct{}{}
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.conns, client); e.mu.Unlock() }()
	_, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err != nil || buffer.Flush() != nil {
		return
	}
	_ = tunengine.RelayHalfClosedTCP(&hijackedConn{Conn: client, reader: buffer.Reader}, upstream, 30*time.Second)
}

// Hijack may already have read tunnel payload beyond the CONNECT headers.
// Read that buffer first while writes, close and deadlines use the raw socket.
type hijackedConn struct {
	net.Conn
	reader io.Reader
}

func (c *hijackedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

var _ engine.Engine = (*Local)(nil)

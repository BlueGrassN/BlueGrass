package bridge

import (
	"bluegrass/native/core/protocolconfig"
	"context"
	tun "github.com/xtls/xray-core/proxy/tun"
	"net/netip"
	"time"
)

// Kotlin retains the original FD. Only our duplicate is consumed and closed by
// the standalone TUN device; all proxy protocols are implemented by Xray-core.
func Start(raw string, tunFd int32, platform Platform) (*Session, error) {
	if tunFd <= 0 || platform == nil {
		return nil, errConfig
	}
	c, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}
	entry, err := resolveEntry(platform, c.Host)
	if err != nil {
		return nil, err
	}
	rawNetwork, err := platform.NetworkInfo()
	if err != nil {
		return nil, errNetwork
	}
	info, err := parseNetwork(rawNetwork)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	lease := newSessionLease(func() bool { return ctx.Err() == nil && platform.IsValid() })
	p := &platformBridge{callback: platform, info: info, fd: int(tunFd), lease: lease}
	ctx = context.WithValue(ctx, platformKey{}, p)
	s := &Session{ctx: ctx, cancel: cancel, lease: lease, platform: p}
	fail := func(err error) (*Session, error) { _ = s.Close(); return nil, err }
	// The shared adapter constructs a real Xray instance with this session context.
	out, err := protocolconfig.NewOutbound(ctx, c.Protocol, netip.AddrPortFrom(entry, uint16(c.Port)))
	if err != nil {
		return fail(err)
	}
	s.box = out
	s.selected = out
	if c.Protocol.Type == "socks5" {
		guarded, err := newGuardedSOCKS(ctx, socksOptions{Server: entry.String(), ServerPort: uint16(c.Port), Username: c.Protocol.SOCKS5.Username, Password: c.Protocol.SOCKS5.Password})
		if err != nil {
			return fail(errStart)
		}
		s.selected = socksStreamDialer{guarded}
	}
	s.resolver, err = newDNSRouter(ctx, p, s.selected, c.RoutingMode)
	if err != nil {
		return fail(errStart)
	}
	device, err := openMobileTun(int(tunFd))
	if err != nil {
		return fail(errStart)
	}
	p.device = device
	s.stack, err = tun.NewStack(ctx, tun.StackOptions{Tun: device, MTU: 1500, IdleTimeout: 5 * time.Minute}, &flowHandler{session: s})
	if err != nil {
		return fail(errStart)
	}
	if device.Start() != nil || s.stack.Start() != nil || !lease.allowed() {
		return fail(errStart)
	}
	activeRuleSession.Store(s)
	return s, nil
}

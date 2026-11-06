// Package bridge embeds Xray-core behind a small gomobile API.
package bridge

import (
	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/gate"
	"context"
	"errors"
	xcore "github.com/xtls/xray-core/core"
	tun "github.com/xtls/xray-core/proxy/tun"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

// Platform belongs to one VpnService physical Network. Protect protects AND
// binds the socket; IsValid uses Android elapsedRealtime, including suspend.
type Platform interface {
	IsValid() bool
	Protect(fd int32) error
	Resolve(host string) (string, error)
	NetworkInfo() (string, error)
}
type streamDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}
type Session struct {
	traffic  trafficCounters
	mu       sync.Mutex
	box      io.Closer
	stack    tun.Stack
	ctx      context.Context
	cancel   context.CancelFunc
	selected streamDialer
	resolver *dnsRouter
	closeMu  sync.Mutex
	lease    *sessionLease
	platform *platformBridge
}

func Version() string { return "Xray-core " + xcore.Version() + " / bridge 2" }

func (s *Session) Close() error {
	activeRuleSession.CompareAndSwap(s, nil)
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	instance, cancel := s.box, s.cancel
	s.selected = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	failed := s.lease.close() != nil
	if s.resolver != nil {
		if s.resolver.Close() != nil {
			failed = true
		}
	}
	if s.stack != nil {
		if s.stack.Close() != nil {
			failed = true
		} else {
			s.stack = nil
		}
	}
	if instance != nil {
		if err := instance.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			failed = true
		} else {
			s.mu.Lock()
			s.box = nil
			s.mu.Unlock()
		}
	}
	if s.platform != nil && s.platform.closeTun() != nil {
		failed = true
	}
	if failed {
		return errors.New("CORE_CLOSE_FAILED")
	}
	s.traffic.freeze()
	return nil
}

// ObserveExit only uses the selected outbound. Failure is informational.
func (s *Session) ObserveExit() (string, error) {
	s.mu.Lock()
	ctx, out, resolver := s.ctx, s.selected, s.resolver
	s.mu.Unlock()
	if out == nil || ctx == nil || ctx.Err() != nil || !s.lease.allowed() {
		return "", errClosed
	}
	value, err := exitprobe.Probe(ctx, probeDialer(resolver, out), exitprobe.Options{})
	if err != nil || !gate.PublicIPv4(value) || ctx.Err() != nil || !s.lease.allowed() {
		return "", errors.New("EXIT_PROBE_FAILED")
	}
	return value, nil
}
func resolveEntry(platform Platform, host string) (netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if gate.PublicIPv4(addr.String()) {
			return addr, nil
		}
		return netip.Addr{}, errConfig
	}
	type resolved struct {
		value string
		err   error
	}
	result := make(chan resolved, 1)
	go func() { v, e := platform.Resolve(host); result <- resolved{v, e} }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case r := <-result:
		if r.err != nil || len(r.value) > 4096 {
			return netip.Addr{}, errNetwork
		}
		for _, candidate := range strings.Split(r.value, ",") {
			if gate.PublicIPv4(candidate) {
				return netip.ParseAddr(candidate)
			}
		}
		return netip.Addr{}, errNetwork
	case <-timer.C:
		return netip.Addr{}, errNetwork
	}
}

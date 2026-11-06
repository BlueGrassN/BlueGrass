package bridge

import (
	"bluegrass/native/core/gate"
	"bluegrass/native/core/routingpolicy"
	"bluegrass/native/core/routingupdate"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

var ruleManager struct {
	sync.Mutex
	path  string
	store *routingupdate.Store
}
var activeRuleSession atomic.Pointer[Session]

// ConfigureRoutingRules uses only the private directory supplied by Android.
// Failure leaves the verified bundled fallback available to VPN sessions.
func ConfigureRoutingRules(directory string) error {
	ruleManager.Lock()
	defer ruleManager.Unlock()
	directory = filepath.Clean(directory)
	if ruleManager.store != nil {
		if ruleManager.path != directory {
			return errors.New("UPDATE_FAILED")
		}
		return nil
	}
	store, err := routingupdate.New(directory)
	if err != nil {
		return errors.New("UPDATE_FAILED")
	}
	ruleManager.path, ruleManager.store = directory, store
	return nil
}
func configuredRules() *routingupdate.Store {
	ruleManager.Lock()
	defer ruleManager.Unlock()
	return ruleManager.store
}
func currentRoutingRules() (*routingpolicy.Rules, error) {
	if store := configuredRules(); store != nil {
		rules, _ := store.Snapshot()
		return rules, nil
	}
	return routingpolicy.Load()
}
func fallbackRuleStatus() routingupdate.Status {
	return routingupdate.Status{Version: strings.TrimSuffix(strings.TrimPrefix(routingpolicy.Version, "loyalsoldier-"), "_v1"), Source: "bundled"}
}
func encodedRuleStatus(state routingupdate.Status) string {
	data, _ := json.Marshal(state)
	return string(data)
}
func RoutingRulesStatus() string {
	if store := configuredRules(); store != nil {
		return encodedRuleStatus(store.Status())
	}
	return encodedRuleStatus(fallbackRuleStatus())
}
func CancelRoutingRuleUpdate() {
	if store := configuredRules(); store != nil {
		store.Cancel()
	}
}

// UpdateRoutingRules runs on a Kotlin worker. It never reconnects or mutates an
// active VPN's DNS/FakeIP decisions. HTTP errors cannot expose resource secrets.
func UpdateRoutingRules(force bool) string {
	store := configuredRules()
	if store == nil {
		state := fallbackRuleStatus()
		state.LastError = "UPDATE_FAILED"
		return encodedRuleStatus(state)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var dial func(context.Context, string, string) (net.Conn, error)
	if session := activeRuleSession.Load(); session != nil {
		session.mu.Lock()
		out, resolver, sessionCtx := session.selected, session.resolver, session.ctx
		session.mu.Unlock()
		if out != nil && resolver != nil && sessionCtx != nil {
			stop := context.AfterFunc(sessionCtx, cancel)
			defer stop()
			dial = routingUpdateDialer(resolver, out, func() bool { return sessionCtx.Err() == nil && session.lease.allowed() })
		}
	}
	client := routingupdate.NewHTTPClient(dial)
	defer client.CloseIdleConnections()
	return encodedRuleStatus(store.Update(ctx, client, force))
}

func routingUpdateDialer(resolver publicResolver, out streamDialer, valid func() bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || network != "tcp" || port != "443" || !routingupdate.AllowedURL("https://"+host+"/") || ctx.Err() != nil || !valid() {
			return nil, errors.New("UPDATE_FAILED")
		}
		ips, err := resolver.LookupPublic(ctx, host)
		if err != nil {
			return nil, errors.New("UPDATE_FAILED")
		}
		for _, ip := range ips {
			if !gate.PublicIPv4(ip.String()) {
				continue
			}
			if ctx.Err() != nil || !valid() {
				break
			}
			conn, err := out.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
			if err == nil && conn != nil {
				if ctx.Err() == nil && valid() {
					return conn, nil
				}
				conn.Close()
			}
		}
		return nil, errors.New("UPDATE_FAILED")
	}
}

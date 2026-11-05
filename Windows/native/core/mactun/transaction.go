package mactun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/netip"
	"reflect"
	"strings"
)

type manager struct {
	b Backend
	j Journal
}

func newManager(uid uint32, b Backend) (*manager, error) {
	if uid == 0 {
		return nil, ErrOwner
	}
	j, err := b.LoadJournal()
	if err != nil {
		return nil, err
	}
	if j != nil {
		if j.OwnerUID != uid {
			return nil, ErrOwner
		}
		if err = validateJournal(*j); err != nil {
			return nil, err
		}
		return &manager{b: b, j: *j}, nil
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return nil, err
	}
	m := &manager{b: b, j: Journal{Version: journalVersion, OwnerUID: uid, ID: hex.EncodeToString(id)}}
	return m, nil
}
func validateJournal(j Journal) error {
	if j.Version != journalVersion || j.OwnerUID == 0 || len(j.ID) != 32 || !opaqueID.MatchString(j.ID) || len(j.Routes) > maxPermits+16 || len(j.DNS) > 32 || len(j.Rules) > 131072 || len(j.PendingRules) > 131072 || j.Session != "" && !opaqueID.MatchString(j.Session) || j.ResourceHash != "" && !digestPattern.MatchString(j.ResourceHash) {
		return ErrRecovery
	}
	if j.Token != "" {
		for _, c := range j.Token {
			if c < '0' || c > '9' {
				return ErrRecovery
			}
		}
		if len(j.Token) > 20 {
			return ErrRecovery
		}
	}
	seen := map[netip.Prefix]bool{}
	for _, r := range j.Routes {
		if !validRoute(r) || seen[r.Prefix] {
			return ErrRecovery
		}
		seen[r.Prefix] = true
	}
	services := map[string]bool{}
	for _, d := range j.DNS {
		if !validService(d.Before.Service) || !interfacePattern.MatchString(d.Before.Interface) || !validAddresses(d.Before.Servers) || !validAddresses(d.Before.Effective) || !validAddresses(d.Before.DHCP) || !validAddresses(d.After) || len(d.After) != 1 || services[d.Before.Service] {
			return ErrRecovery
		}
		services[d.Before.Service] = true
		// Only the adapter's three fixed candidate DNS peers can be restored.
		switch d.After[0].String() {
		case "172.31.255.1", "172.30.255.1", "10.255.255.1":
		default:
			return ErrRecovery
		}
	}
	for _, rules := range []string{j.Rules, j.PendingRules} {
		if rules != "" && validateGuardRules(rules) != nil {
			return ErrRecovery
		}
	}
	return nil
}
func (m *manager) save() error { return m.b.SaveJournal(m.j) }
func (m *manager) guard(ctx context.Context, g Guard) error {
	raw, err := renderGuard(g)
	if err != nil {
		return err
	}
	if err = m.b.CheckPF(ctx, !m.j.Active); err != nil {
		return err
	}
	canonical, err := m.b.CompilePF(ctx, raw)
	if err != nil {
		return err
	}
	if err = validateGuardRules(canonical); err != nil {
		return err
	}
	current, err := m.b.ReadPF(ctx)
	if err != nil {
		return err
	}
	if current != m.j.Rules && (m.j.PendingRules == "" || current != m.j.PendingRules) && !(m.j.Active && current == "") {
		return ErrExternalWriter
	}
	if m.j.Token != "" {
		exists, e := m.b.TokenExists(ctx, m.j.Token)
		if e != nil {
			return e
		}
		if !exists {
			m.j.Token = ""
		}
	}
	if m.j.Releasing {
		m.j.Releasing = false
	}
	if current == canonical && m.j.Active && m.j.Token != "" && m.j.PendingRules == "" && !m.j.Enabling && !m.j.Releasing {
		return m.verifyReference(ctx)
	}
	m.j.Active = true
	m.j.PendingRules = canonical
	if err = m.save(); err != nil {
		return err
	}
	if err = m.b.Marker(m.j.OwnerUID, true); err != nil {
		return err
	}
	if current != canonical {
		if err = m.b.WritePF(ctx, raw); err != nil {
			return err
		}
	}
	current, err = m.b.ReadPF(ctx)
	if err != nil {
		return err
	}
	if current != canonical {
		return ErrExternalWriter
	}
	m.j.Rules = canonical
	m.j.PendingRules = ""
	if err = m.save(); err != nil {
		return err
	}
	if m.j.Token == "" {
		// An interrupted -E cannot safely be retried: the missing token represents
		// an unrecovered owned reference, never permission to disable global PF.
		if m.j.Enabling {
			return ErrRecovery
		}
		m.j.Enabling = true
		if err = m.save(); err != nil {
			return err
		}
		token, e := m.b.EnablePF(ctx)
		if e != nil {
			return e
		}
		m.j.Token = token
		m.j.Enabling = false
		if err = m.save(); err != nil {
			return err
		}
	}
	return m.verifyReference(ctx)
}
func (m *manager) verifyReference(ctx context.Context) error {
	if m.j.Token == "" {
		return ErrRecovery
	}
	exists, err := m.b.TokenExists(ctx, m.j.Token)
	if err != nil {
		return err
	}
	if !exists {
		return ErrRecovery
	}
	return m.b.PFEnabled(ctx)
}
func (m *manager) verifyGuard(ctx context.Context) error {
	if !m.j.Active || m.j.Token == "" || m.j.PendingRules != "" {
		return ErrRecovery
	}
	if err := m.b.CheckPF(ctx, false); err != nil {
		return err
	}
	rules, err := m.b.ReadPF(ctx)
	if err != nil {
		return err
	}
	if rules != m.j.Rules {
		return ErrExternalWriter
	}
	return m.verifyReference(ctx)
}

// Close prioritizes the durable narrowing transaction over expensive global
// topology inspection. These are already compiled, owned stateless rules.
// Removing only tunnel/root-peer passes cannot open a new path; owner HTTPS
// and scoped LAN survive. Global hook/reference verification follows the write.
func (m *manager) blockFast(ctx context.Context, fallback Guard) error {
	current, err := m.b.ReadPF(ctx)
	if err != nil {
		return err
	}
	if current == "" {
		return m.guard(ctx, fallback)
	}
	if current != m.j.Rules && (m.j.PendingRules == "" || current != m.j.PendingRules) {
		return ErrExternalWriter
	}
	if err = validateGuardRules(current); err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(strings.TrimSpace(current), "\n") {
		if strings.Contains(line, `label "bluegrass-tunnel"`) || strings.Contains(line, `label "bluegrass-peer"`) {
			continue
		}
		keep = append(keep, line)
	}
	blocking := strings.Join(keep, "\n") + "\n"
	if err = validateGuardRules(blocking); err != nil {
		return err
	}
	if current != blocking {
		m.j.PendingRules = blocking
		if err = m.save(); err != nil {
			return err
		}
		if err = m.b.WritePF(ctx, blocking); err != nil {
			return err
		}
		actual, e := m.b.ReadPF(ctx)
		if e != nil {
			return e
		}
		if actual != blocking {
			return ErrExternalWriter
		}
		m.j.Rules = blocking
		m.j.PendingRules = ""
		if err = m.save(); err != nil {
			return err
		}
	}
	return m.verifyGuard(ctx)
}
func (m *manager) addRoute(ctx context.Context, r Route) error {
	if !validRoute(r) {
		return errors.New("INVALID_OWNED_ROUTE")
	}
	for _, owned := range m.j.Routes {
		if owned.Prefix == r.Prefix {
			if owned != r {
				return ErrExternalWriter
			}
			return m.verifyRoute(ctx, r)
		}
	}
	before, err := m.b.ReadRoute(ctx, r.Prefix)
	if err != nil {
		return err
	}
	// Even an identical foreign route is not ours to delete. Borrowing it is
	// unnecessary: refusing prevents ambiguous recovery after another writer.
	if before != nil {
		return ErrExternalWriter
	}
	m.j.Routes = append(m.j.Routes, r)
	if err = m.save(); err != nil {
		m.j.Routes = m.j.Routes[:len(m.j.Routes)-1]
		return err
	}
	if err = m.b.AddRoute(ctx, r); err != nil {
		return err
	}
	return m.verifyRoute(ctx, r)
}
func (m *manager) verifyRoute(ctx context.Context, r Route) error {
	current, err := m.b.ReadRoute(ctx, r.Prefix)
	if err != nil {
		return err
	}
	if current == nil || *current != r {
		return ErrExternalWriter
	}
	return nil
}
func (m *manager) setDNS(ctx context.Context, r Resolver, after []netip.Addr) error {
	if !validService(r.Service) || !interfacePattern.MatchString(r.Interface) || !validAddresses(r.Servers) || !validAddresses(r.Effective) || len(after) != 1 {
		return ErrRecovery
	}
	for _, d := range m.j.DNS {
		if d.Before.Service == r.Service {
			return ErrRecovery
		}
	}
	current, err := m.b.ReadDNS(ctx, r.Service)
	if err != nil {
		return err
	}
	if !sameAddresses(current, r.Servers) {
		return ErrExternalWriter
	}
	m.j.DNS = append(m.j.DNS, DNSChange{Before: r, After: append([]netip.Addr(nil), after...)})
	if err = m.save(); err != nil {
		m.j.DNS = m.j.DNS[:len(m.j.DNS)-1]
		return err
	}
	if err = m.b.WriteDNS(ctx, r.Service, after); err != nil {
		return err
	}
	current, err = m.b.ReadDNS(ctx, r.Service)
	if err != nil {
		return err
	}
	if !sameAddresses(current, after) {
		return ErrExternalWriter
	}
	return nil
}
func sameAddresses(a, b []netip.Addr) bool {
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b)
}

// Conflicts deliberately retain the checkpoint. No foreign setting is changed,
// and Release cannot remove protection until every owned setting is recovered.
func (m *manager) restore(ctx context.Context) error {
	var all error
	for i := len(m.j.DNS) - 1; i >= 0; i-- {
		d := m.j.DNS[i]
		current, err := m.b.ReadDNS(ctx, d.Before.Service)
		if err == nil && !sameAddresses(current, d.Before.Servers) {
			if !sameAddresses(current, d.After) {
				err = ErrExternalWriter
			} else {
				err = m.b.WriteDNS(ctx, d.Before.Service, d.Before.Servers)
				if err == nil {
					current, err = m.b.ReadDNS(ctx, d.Before.Service)
					if err == nil && !sameAddresses(current, d.Before.Servers) {
						err = ErrExternalWriter
					}
				}
			}
		}
		if err != nil {
			all = errors.Join(all, err)
			continue
		}
		old := append([]DNSChange(nil), m.j.DNS...)
		m.j.DNS = append(m.j.DNS[:i], m.j.DNS[i+1:]...)
		if err = m.save(); err != nil {
			m.j.DNS = old
			return errors.Join(all, err)
		}
	}
	for i := len(m.j.Routes) - 1; i >= 0; i-- {
		r := m.j.Routes[i]
		current, err := m.b.ReadRoute(ctx, r.Prefix)
		if err == nil && current != nil {
			if *current != r {
				err = ErrExternalWriter
			} else {
				err = m.b.DeleteRoute(ctx, r)
				if err == nil {
					current, err = m.b.ReadRoute(ctx, r.Prefix)
					if err == nil && current != nil {
						err = ErrExternalWriter
					}
				}
			}
		}
		if err != nil {
			all = errors.Join(all, err)
			continue
		}
		old := append([]Route(nil), m.j.Routes...)
		m.j.Routes = append(m.j.Routes[:i], m.j.Routes[i+1:]...)
		if err = m.save(); err != nil {
			m.j.Routes = old
			return errors.Join(all, err)
		}
	}
	return all
}
func (m *manager) dnsOverrides(ctx context.Context) (map[string][]netip.Addr, error) {
	overrides := map[string][]netip.Addr{}
	for _, d := range m.j.DNS {
		if err := m.b.VerifyResolver(ctx, d.Before); err != nil {
			return nil, err
		}
		current, err := m.b.ReadDNS(ctx, d.Before.Service)
		if err != nil {
			return nil, err
		}
		if !sameAddresses(current, d.After) {
			return nil, ErrExternalWriter
		}
		if previous, ok := overrides[d.Before.Interface]; ok && !sameAddresses(previous, d.Before.Effective) {
			return nil, ErrRecovery
		}
		overrides[d.Before.Interface] = append([]netip.Addr(nil), d.Before.Effective...)
	}
	return overrides, nil
}
func (m *manager) release(ctx context.Context) error {
	if err := m.restore(ctx); err != nil {
		return err
	}
	current, err := m.b.ReadPF(ctx)
	if err != nil {
		return err
	}
	if current != "" && current != m.j.Rules && current != m.j.PendingRules {
		return ErrExternalWriter
	}
	if m.j.Enabling {
		return ErrRecovery
	}
	m.j.Releasing = true
	if err = m.save(); err != nil {
		return err
	}
	if current != "" {
		if err = m.b.WritePF(ctx, ""); err != nil {
			return err
		}
		current, err = m.b.ReadPF(ctx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(current) != "" {
			return ErrExternalWriter
		}
	}
	m.j.Rules = ""
	m.j.PendingRules = ""
	if err = m.save(); err != nil {
		return err
	}
	if m.j.Token != "" {
		m.j.Releasing = true
		if err = m.save(); err != nil {
			return err
		}
		if err = m.b.ReleasePF(ctx, m.j.Token); err != nil {
			return err
		}
		m.j.Token = ""
		if err = m.save(); err != nil {
			return err
		}
	}
	if err = m.b.Marker(m.j.OwnerUID, false); err != nil {
		return err
	}
	if err = m.b.RemoveJournal(); err != nil {
		return err
	}
	m.j = Journal{Version: journalVersion, OwnerUID: m.j.OwnerUID, ID: m.j.ID}
	return nil
}

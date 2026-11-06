// Package macproxy implements ownership-checked macOS proxy transactions. The
// privileged OS boundary is a narrowly scoped Swift subprocess, never a shell.
package macproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"bluegrass/native/core/winproxy"
)

type Settings map[string]any
type Snapshot struct {
	Active   []string            `json:"active"`
	Services map[string]Settings `json:"services"`
}
type Change struct {
	Original Settings `json:"original"`
	Applied  Settings `json:"applied"`
}
type Record struct {
	Version int               `json:"version"`
	Phase   string            `json:"phase"`
	Active  []string          `json:"active"`
	Entries map[string]Change `json:"entries"`
}

// CAS must compare the full observed snapshot under the native preferences lock,
// then commit/apply only differing proxy fields. Save must be durable before CAS.
type Driver interface {
	Read() (Snapshot, error)
	Load() (*Record, error)
	Save(Record) error
	Delete() error
	CAS(expected, desired Snapshot) error
}
type Manager struct {
	gate   chan struct{}
	driver Driver
}

const (
	ApplyBudget    = 25 * time.Second
	RecoveryBudget = 25 * time.Second
	// Polling must wait behind Apply's interactive authorization, rather than
	// converting a valid in-flight connection into an ownership failure.
	StateBudget   = 30 * time.Second
	CleanupBudget = 3 * time.Second
	// Included in the public transaction budget, not added after it.
	helperReapBudget = time.Second
)

func NewWithDriver(d Driver) *Manager { return &Manager{driver: d, gate: make(chan struct{}, 1)} }

type operationDriver interface {
	beginOperation(time.Time)
	endOperation()
}

func (m *Manager) beginOperation(budget time.Duration) (func(), error) {
	reserve := helperReapBudget
	if budget <= helperReapBudget {
		reserve = budget / 10
	}
	deadline := time.Now().Add(budget - reserve)
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case m.gate <- struct{}{}:
	case <-timer.C:
		return nil, winproxy.ErrRecoveryRequired
	}
	if !time.Now().Before(deadline) {
		<-m.gate
		return nil, winproxy.ErrRecoveryRequired
	}
	if d, ok := m.driver.(operationDriver); ok {
		d.beginOperation(deadline)
	}
	return func() {
		if d, ok := m.driver.(operationDriver); ok {
			d.endOperation()
		}
		<-m.gate
	}, nil
}
func encode(v any) ([]byte, error) { return json.Marshal(v) }
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return winproxy.ErrInvalidRecord
	}
	return nil
}
func copySettings(s Settings) Settings {
	c := Settings{}
	for k, v := range s {
		c[k] = v
	}
	return c
}
func equal(a, b any) bool {
	x, e := encode(a)
	y, f := encode(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}
func sameActive(a, b []string) bool {
	x := append([]string{}, a...)
	y := append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	return reflect.DeepEqual(x, y)
}
func (m *Manager) load() (*Record, error) {
	r, err := m.driver.Load()
	if err != nil || r == nil {
		return r, err
	}
	if r.Version != 1 || (r.Phase != "prepared" && r.Phase != "applied" && r.Phase != "restoring") || len(r.Entries) == 0 || len(r.Entries) > 256 {
		return nil, winproxy.ErrInvalidRecord
	}
	for id, c := range r.Entries {
		if id == "" || c.Original == nil || c.Applied == nil || !validApplied(c.Applied) {
			return nil, winproxy.ErrInvalidRecord
		}
		if !equal(c.Original["ProxyAutoConfigURLString"], c.Applied["ProxyAutoConfigURLString"]) {
			return nil, winproxy.ErrInvalidRecord
		}
	}
	if len(r.Active) != len(r.Entries) {
		return nil, winproxy.ErrInvalidRecord
	}
	seen := map[string]bool{}
	for _, id := range r.Active {
		if _, ok := r.Entries[id]; !ok || seen[id] {
			return nil, winproxy.ErrInvalidRecord
		}
		seen[id] = true
	}
	return r, nil
}

func validApplied(s Settings) bool {
	for k, v := range map[string]any{
		"HTTPEnable": 1, "HTTPSEnable": 1, "HTTPProxy": "127.0.0.1", "HTTPSProxy": "127.0.0.1",
		"HTTPPort": 12808, "HTTPSPort": 12808, "ProxyAutoConfigEnable": 0, "ProxyAutoDiscoveryEnable": 0, "SOCKSEnable": 0,
	} {
		if !equal(s[k], v) {
			return false
		}
	}
	if !equal(s["ExcludeSimpleHostnames"], 0) && !equal(s["ExcludeSimpleHostnames"], 1) {
		return false
	}
	b, err := encode(s["ExceptionsList"])
	var exceptions []string
	if err != nil || len(b) == 0 || b[0] != '[' || decode(b, &exceptions) != nil || len(exceptions) > 512 {
		return false
	}
	return true
}
func target(original Settings, bypass string) Settings {
	s := copySettings(original)
	for _, k := range []string{"HTTPEnable", "HTTPSEnable"} {
		s[k] = float64(1)
	}
	for _, k := range []string{"HTTPProxy", "HTTPSProxy"} {
		s[k] = "127.0.0.1"
	}
	for _, k := range []string{"HTTPPort", "HTTPSPort"} {
		s[k] = float64(12808)
	}
	for _, k := range []string{"ProxyAutoConfigEnable", "ProxyAutoDiscoveryEnable", "SOCKSEnable"} {
		s[k] = float64(0)
	}
	exceptions := []string{}
	simple := float64(0)
	for _, v := range strings.Split(bypass, ";") {
		if v == "<local>" {
			simple = 1
		} else if v != "" {
			if v == "[::1]" {
				v = "::1"
			}
			exceptions = append(exceptions, v)
		}
	}
	s["ExceptionsList"] = exceptions
	s["ExcludeSimpleHostnames"] = simple
	return s
}
func (m *Manager) Apply(address, bypass string) error {
	if address != "127.0.0.1:12808" || len(bypass) > 4096 || strings.ContainsAny(bypass, "\x00\r\n") {
		return winproxy.ErrInvalidSettings
	}
	end, lockErr := m.beginOperation(ApplyBudget)
	if lockErr != nil {
		return lockErr
	}
	defer end()
	r, err := m.load()
	if err != nil {
		return err
	}
	s, err := m.driver.Read()
	if err != nil {
		return err
	}
	if r != nil {
		if !owned(s, r) {
			return winproxy.ErrOwnershipLost
		}
		if r.Phase != "applied" {
			return winproxy.ErrRecoveryRequired
		}
		for _, c := range r.Entries {
			if !equal(c.Applied, target(c.Original, bypass)) {
				return winproxy.ErrRecoveryRequired
			}
		}
		return nil
	}
	if len(s.Active) == 0 || len(s.Active) > 256 {
		return winproxy.ErrInvalidSettings
	}
	r = &Record{Version: 1, Phase: "prepared", Active: append([]string{}, s.Active...), Entries: map[string]Change{}}
	desired := Snapshot{Active: s.Active, Services: map[string]Settings{}}
	for id, values := range s.Services {
		desired.Services[id] = copySettings(values)
	}
	for _, id := range s.Active {
		original, ok := s.Services[id]
		if !ok {
			return winproxy.ErrInvalidSettings
		}
		applied := target(original, bypass)
		r.Entries[id] = Change{Original: copySettings(original), Applied: applied}
		desired.Services[id] = applied
	}
	if err = m.driver.Save(*r); err != nil {
		return err
	}
	if err = m.driver.CAS(s, desired); err != nil {
		return errors.Join(err, m.restore())
	}
	verified, err := m.driver.Read()
	if err == nil && !owned(verified, r) {
		err = winproxy.ErrOwnershipLost
	}
	if err != nil {
		return errors.Join(err, m.restore())
	}
	r.Phase = "applied"
	if err = m.driver.Save(*r); err != nil {
		return errors.Join(err, m.restore())
	}
	return nil
}
func owned(s Snapshot, r *Record) bool {
	if !sameActive(s.Active, r.Active) {
		return false
	}
	for id, c := range r.Entries {
		if !equal(s.Services[id], c.Applied) {
			return false
		}
	}
	return true
}

// Dependent settings form an ownership unit. A changed endpoint relinquishes
// its enable flag too; restoring individual matching keys could disable another
// application's proxy or re-enable its PAC/SOCKS configuration.
var ownershipGroups = [][]string{
	{"HTTPEnable", "HTTPProxy", "HTTPPort"},
	{"HTTPSEnable", "HTTPSProxy", "HTTPSPort"},
	{"SOCKSEnable", "SOCKSProxy", "SOCKSPort"},
	{"ProxyAutoConfigEnable", "ProxyAutoConfigURLString"},
	{"ProxyAutoDiscoveryEnable"},
	{"ExceptionsList", "ExcludeSimpleHostnames"},
}

func externalManualPolicy(current Settings, c Change) bool {
	for _, keys := range ownershipGroups[:3] {
		if !sameGroup(current, c.Applied, keys) && !sameGroup(current, c.Original, keys) {
			return true
		}
	}
	return false
}

// Re-enabling old PAC, discovery, or SOCKS can override a competing manual
// endpoint. Leave those selection modes unchanged after manual ownership loss.
func preserveSelection(keys []string, current Settings, c Change) bool {
	return externalManualPolicy(current, c) && (keys[0] == "ProxyAutoConfigEnable" || keys[0] == "ProxyAutoDiscoveryEnable" || keys[0] == "SOCKSEnable")
}

// Group ownership may be ambiguous (for example an external enable flag with
// our unchanged endpoint). Do not erase the only recovery record while any
// endpoint still points to the listener we are about to close. Fully original
// settings are resolved, even if the original itself used that address.
func unresolvedLoopback(s Snapshot, r *Record) bool {
	for id, c := range r.Entries {
		current := s.Services[id]
		for _, keys := range ownershipGroups[:2] {
			if sameGroup(c.Original, c.Applied, keys) || sameGroup(current, c.Original, keys) {
				continue
			}
			if sameGroup(current, c.Applied, keys[1:]) {
				return true
			}
		}
	}
	return false
}

func sameGroup(a, b Settings, keys []string) bool {
	for _, k := range keys {
		av, ap := a[k]
		bv, bp := b[k]
		if ap != bp || !equal(av, bv) {
			return false
		}
	}
	return true
}
func (m *Manager) State() (bool, error) {
	end, lockErr := m.beginOperation(StateBudget)
	if lockErr != nil {
		return false, lockErr
	}
	defer end()
	r, err := m.load()
	if err != nil || r == nil {
		return false, err
	}
	s, err := m.driver.Read()
	if err != nil {
		return false, err
	}
	if !owned(s, r) {
		return false, winproxy.ErrOwnershipLost
	}
	if r.Phase != "applied" {
		return false, winproxy.ErrRecoveryRequired
	}
	return true, nil
}
func (m *Manager) Restore() error {
	end, err := m.beginOperation(CleanupBudget)
	if err != nil {
		return err
	}
	defer end()
	return m.restore()
}
func (m *Manager) Recover() error {
	end, err := m.beginOperation(RecoveryBudget)
	if err != nil {
		return err
	}
	defer end()
	return m.restore()
}
func (m *Manager) restore() error {
	r, err := m.load()
	if err != nil || r == nil {
		return err
	}
	s, err := m.driver.Read()
	if err != nil {
		return err
	}
	desired := Snapshot{Active: s.Active, Services: map[string]Settings{}}
	for id, v := range s.Services {
		desired.Services[id] = copySettings(v)
	}
	lost := false
	for id, c := range r.Entries {
		current, exists := s.Services[id]
		if !exists {
			lost = true
			continue
		}
		for _, keys := range ownershipGroups {
			if sameGroup(c.Original, c.Applied, keys) || sameGroup(current, c.Original, keys) {
				continue
			}
			if !sameGroup(current, c.Applied, keys) {
				lost = true
				continue
			}
			if preserveSelection(keys, current, c) {
				lost = true
				continue
			}
			for _, k := range keys {
				if value, present := c.Original[k]; present {
					desired.Services[id][k] = value
				} else {
					delete(desired.Services[id], k)
				}
			}
		}
	}
	r.Phase = "restoring"
	if err = m.driver.Save(*r); err != nil {
		return err
	}
	// Even a no-op CAS re-applies committed preferences after a previous native
	// ApplyChanges failure, and verifies there was no intervening writer.
	if err = m.driver.CAS(s, desired); err != nil {
		return err
	}
	actual, err := m.driver.Read()
	if err != nil {
		return err
	}
	if !equal(actual, desired) {
		return winproxy.ErrVerificationFailed
	}
	if unresolvedLoopback(actual, r) {
		return winproxy.ErrRecoveryRequired
	}
	if err = m.driver.Delete(); err != nil {
		return err
	}
	if lost {
		return winproxy.ErrOwnershipLost
	}
	return nil
}
func (m *Manager) Relinquish() error {
	end, lockErr := m.beginOperation(CleanupBudget)
	if lockErr != nil {
		return lockErr
	}
	defer end()
	r, err := m.load()
	if err != nil || r == nil {
		return err
	}
	s, err := m.driver.Read()
	if err != nil {
		return err
	}
	if unresolvedLoopback(s, r) {
		return winproxy.ErrRecoveryRequired
	}
	for id, c := range r.Entries {
		for _, keys := range ownershipGroups {
			if !sameGroup(c.Original, c.Applied, keys) && sameGroup(s.Services[id], c.Applied, keys) && !preserveSelection(keys, s.Services[id], c) {
				return winproxy.ErrRecoveryRequired
			}
		}
	}
	return m.driver.Delete()
}

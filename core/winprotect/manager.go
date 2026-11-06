// Package winprotect owns BlueGrass's persistent WFP protection. Callers must
// authenticate IPC owners and verify the current tunnel session before allowing it.
package winprotect

import (
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/gate"
)

var (
	ErrClosed           = errors.New("protection manager is closed")
	ErrOwner            = errors.New("protection belongs to another or unknown Windows owner")
	ErrRecoveryRequired = errors.New("protection requires explicit recovery or a fresh Arm")
	ErrNotBlocked       = errors.New("Block must succeed before changing the protection session")
)

type intent struct {
	Version          int                `json:"version"`
	ProtectIntent    bool               `json:"protectIntent"`
	OwnerSID         string             `json:"ownerSid"`
	AccountKey       string             `json:"accountKey"`
	ResourceID       string             `json:"resourceId"`
	Mode             string             `json:"mode"`
	Since            string             `json:"since"`
	Recovery         bool               `json:"recoveryRequired"`
	ResourceKey      engine.ResourceKey `json:"resourceKey"`
	LogicalSessionID string             `json:"logicalSessionId"`
	Phase            string             `json:"phase"`
	CleanupRequired  bool               `json:"cleanupRequired"`
	Revision         uint64             `json:"revision"`
}

type intentStore interface {
	Load() (*intent, error)
	Save(intent) error
	Close() error
}
type firewall interface {
	Present() (bool, error)
	Replace([]rule) error
	Remove() error
	Close() error
}

// DNSEndpoint is an exact trusted service DNS permission. Transport is strictly
// "tcp" or "udp"; Endpoint must be a numeric, unscoped address and nonzero port.
// Unlike entrydns.Candidate, this API has no default port or resolver fallback.
type DNSEndpoint struct {
	Endpoint  netip.AddrPort
	Transport string
}

// EntryPolicy describes the exact service-owned entry transport. Only SOCKS
// may additionally authorize authenticated UDP ASSOCIATE relay endpoints.
type EntryPolicy struct{ TCP, UDP, AuthenticatedSOCKSRelay bool }

func dualDNS(endpoints []netip.AddrPort) []DNSEndpoint {
	result := make([]DNSEndpoint, 0, 2*len(endpoints))
	for _, endpoint := range endpoints {
		result = append(result, DNSEndpoint{endpoint, "tcp"}, DNSEndpoint{endpoint, "udp"})
	}
	return result
}

// Manager serializes policy changes. Active reports remaining protection
// intent OR a committed filter set, not proof of packet isolation. Failed
// activation/recovery must be surfaced using errors and RecoveryRequired.
type Manager struct {
	mu                                 sync.Mutex
	store                              intentStore
	fw                                 firewall
	network                            func() ([]link, error)
	verifyTunnel                       func(uint64) error
	state                              intent
	policy                             policy
	active, closed, armed, rebindReady bool
	dnsBootstrap                       bool
	runtimeOwned                       bool                      // never recovered from disk
	localPrepared                      bool                      // never a business proof
	udpRelayRefs                       map[netip.AddrPort]uint64 // current associations only; never persisted
	directRefs                         map[directEndpoint]uint64 // current direct connections only; never persisted
}

func newManager(service, ui string, store intentStore, fw firewall, network func() ([]link, error), verify func(uint64) error) (*Manager, error) {
	m := &Manager{store: store, fw: fw, network: network, verifyTunnel: verify, rebindReady: true,
		policy: policy{Service: service, UI: ui, Virtual: defaultVirtualPrefixes()}}
	r, loadErr := store.Load()
	present, err := fw.Present()
	if err != nil {
		return nil, err
	}
	if loadErr == nil && (r == nil || !r.ProtectIntent) && !present {
		if r != nil && validIntent(*r) {
			m.state = *r
		}
		return m, nil
	}
	if r != nil && r.ProtectIntent && validIntent(*r) {
		m.state = *r
	} else {
		m.state = intent{Version: 2, ProtectIntent: true, Mode: "tun", Since: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	if m.state.Version == 1 || m.state.LogicalSessionID == "" {
		m.state.Version = 2
		m.state.Phase = "legacy"
		m.state.CleanupRequired = true
	}
	m.state.Recovery = true
	// Even when recording recovery fails, revoke stale permits. Never release a
	// residual ruleset merely because its journal is absent or unreadable.
	saveErr := m.saveState(m.state)
	blockErr := fw.Replace(compilePolicy(m.policy))
	if blockErr == nil {
		m.active = true
	}
	if err := errors.Join(saveErr, blockErr); err != nil {
		return nil, fmt.Errorf("restore protection: %w", err)
	}
	return m, nil
}

// Arm records non-secret owner/account/resource identifiers before installing
// blocking filters. entry and dns must already be resolved by trusted native
// code. DNS endpoints authorize this service executable only, TCP and UDP.
// Call ArmDNS before resolving an entry hostname on the initial connection.
func (m *Manager) Arm(ownerSID, accountKey, resourceID string, entry netip.AddrPort, dns []netip.AddrPort, allowLAN bool) error {
	return m.ArmWithDNSPolicy(ownerSID, accountKey, resourceID, entry, dualDNS(dns), allowLAN)
}

// ArmDNS installs protection before the service resolves its entry hostname.
// Only the supplied service DNS endpoints and normal control/LAN exceptions
// are allowed. Success permits a full Arm for the same binding without Block,
// but never authorizes a tunnel or a UDP relay. Scoped DNS is still rejected.
func (m *Manager) ArmDNS(ownerSID, accountKey, resourceID string, dns []netip.AddrPort, allowLAN bool) error {
	return m.ArmDNSWithPolicy(ownerSID, accountKey, resourceID, dualDNS(dns), allowLAN)
}

// ArmWithDNSPolicy is Arm with transport-specific DNS permissions. All owner,
// journal, transaction and fresh-session requirements are identical to Arm.
func (m *Manager) ArmWithDNSPolicy(ownerSID, accountKey, resourceID string, entry netip.AddrPort, dns []DNSEndpoint, allowLAN bool) error {
	return m.ArmWithEntryPolicy(ownerSID, accountKey, resourceID, entry, EntryPolicy{TCP: true, AuthenticatedSOCKSRelay: true}, dns, allowLAN)
}

func (m *Manager) ArmWithEntryPolicy(ownerSID, accountKey, resourceID string, entry netip.AddrPort, transports EntryPolicy, dns []DNSEndpoint, allowLAN bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !transports.TCP && !transports.UDP || transports.AuthenticatedSOCKSRelay && (!transports.TCP || transports.UDP) {
		return errors.New("invalid entry transports")
	}
	return m.arm(ownerSID, accountKey, resourceID, entry, transports, dns, allowLAN, false)
}

// ArmDNSWithPolicy bootstraps protection with only the selected DNS transports.
// It accepts at most 64 permissions across 32 distinct numeric endpoints.
func (m *Manager) ArmDNSWithPolicy(ownerSID, accountKey, resourceID string, dns []DNSEndpoint, allowLAN bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.arm(ownerSID, accountKey, resourceID, netip.AddrPort{}, EntryPolicy{}, dns, allowLAN, true)
}

// UpdateControlDNS atomically replaces only fixed control-plane DNS permissions.
// The trusted service must restrict query hostnames before using this method.
// It does not change intent, owner, entry, LAN policy, proof, TUN or UDP relays.
// Recovery remains recovery: this can restore API access, never business access.
func (m *Manager) UpdateControlDNS(dns []DNSEndpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateControlDNS(dns)
}

// UpdateControlDNSForOwner linearizes the helper's OS-owner check with the
// policy write. Inactive protection needs no exception and stays inactive.
// This is the service entrypoint for concurrent control/update resolver clients.
func (m *Manager) UpdateControlDNSForOwner(ownerSID string, dns []DNSEndpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !validSID(ownerSID) {
		return ErrOwner
	}
	if !m.state.ProtectIntent && !m.active {
		return nil
	}
	if m.state.OwnerSID != ownerSID {
		return ErrOwner
	}
	return m.updateControlDNS(dns)
}

func (m *Manager) updateControlDNS(dns []DNSEndpoint) error {
	if m.closed {
		return ErrClosed
	}
	if !m.active || !m.state.ProtectIntent || !validSID(m.state.OwnerSID) {
		return ErrRecoveryRequired
	}
	if err := validateDNS(dns, m.policy.Virtual); err != nil {
		return err
	}
	p := m.policy
	p.ControlDNS = append([]DNSEndpoint(nil), dns...)
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		return fmt.Errorf("replace control DNS permissions: %w", err)
	}
	m.policy = p
	return nil
}

// AllowUpdateHelper pins the installed updater beside the trusted UI. The
// service must verify that exact file's Authenticode publisher first. No path
// is accepted from IPC. The signed helper enforces compiled HTTPS hostnames;
// WFP enforces its exact executable identity and outbound TCP443 only.
func (m *Manager) AllowUpdateHelper() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if m.policy.UI == "" {
		return errors.New("trusted UI path required")
	}
	p := m.policy
	p.UpdateHelper = filepath.Join(filepath.Dir(p.UI), "BlueGrass.Update.exe")
	if m.state.ProtectIntent {
		if !m.active {
			return ErrRecoveryRequired
		}
		if err := m.fw.Replace(compilePolicy(p)); err != nil {
			return err
		}
	}
	m.policy = p
	return nil
}

func validateDNS(dns []DNSEndpoint, virtual []netip.Prefix) error {
	if len(dns) > 64 {
		return errors.New("too many DNS permissions")
	}
	unique := make(map[netip.AddrPort]bool)
	for _, d := range dns {
		if !validEndpoint(d.Endpoint) || d.Transport != "tcp" && d.Transport != "udp" || isVirtual(d.Endpoint.Addr(), virtual) {
			return errors.New("invalid DNS endpoint")
		}
		unique[d.Endpoint] = true
	}
	if len(unique) > 32 {
		return errors.New("too many DNS endpoints")
	}
	return nil
}

// arm runs with m.mu held. Both phases share validation, journal ordering and
// the atomic firewall replacement path; bootstrap only omits the entry permit.
func (m *Manager) arm(ownerSID, accountKey, resourceID string, entry netip.AddrPort, transports EntryPolicy, dns []DNSEndpoint, allowLAN, bootstrap bool) error {
	if m.closed {
		return ErrClosed
	}
	if !validSID(ownerSID) || !validID(accountKey) || !validID(resourceID) || (!bootstrap && !validEndpoint(entry)) || len(dns) > 64 {
		return errors.New("invalid protection identity or endpoints")
	}
	if err := validateDNS(dns, m.policy.Virtual); err != nil {
		return err
	}
	if m.state.ProtectIntent && m.state.OwnerSID != ownerSID {
		return ErrOwner
	}
	if m.state.CleanupRequired || m.state.Phase == "legacy" {
		return ErrRecoveryRequired
	}
	if m.state.LogicalSessionID != "" && (m.state.ResourceKey.AccountKey != accountKey || m.state.ResourceKey.SourceID != resourceID) {
		return ErrOwner
	}
	if !m.rebindReady {
		return ErrNotBlocked
	}
	if m.dnsBootstrap && (m.state.AccountKey != accountKey || m.state.ResourceID != resourceID) {
		return ErrNotBlocked
	}
	links, err := m.network()
	if err != nil {
		return fmt.Errorf("snapshot local interfaces: %w", err)
	}
	p := m.policy
	p.Entry = entry
	p.EntryPolicy = transports
	p.DNS = append([]DNSEndpoint(nil), dns...)
	p.AllowLAN = allowLAN
	p.Links = links
	p.Tunnel = 0
	p.LocalTunnel, p.LocalOnly = 0, false
	p.UDPRelays = nil
	p.Direct = nil
	if isVirtual(entry.Addr(), p.Virtual) {
		return errors.New("entry is a product virtual address")
	}
	next := m.state
	if next.LogicalSessionID == "" {
		next = intent{Version: 2, ProtectIntent: true, OwnerSID: ownerSID, AccountKey: accountKey, ResourceID: resourceID, Mode: "tun", Since: time.Now().UTC().Format(time.RFC3339Nano), Phase: "unbound"}
	} else {
		next.Phase = "blocked"
		next.Recovery = false
	}
	if err := m.saveState(next); err != nil {
		return fmt.Errorf("commit protection intent: %w", err)
	}
	m.policy = p
	m.udpRelayRefs = nil
	m.directRefs = nil
	m.armed = false
	m.rebindReady = false
	m.dnsBootstrap = false
	m.localPrepared = false
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.state.Recovery = true
		return fmt.Errorf("activate protection: %w", err)
	}
	m.active = true
	m.armed = !bootstrap
	m.rebindReady = bootstrap
	m.dnsBootstrap = bootstrap
	return nil
}

// AllowTunnel is a privileged assertion by the service that authentication,
// resource eligibility and exit verification belong to THIS armed session.
// OS inspection also requires the exact live BlueGrass TUN LUID. LUIDs and
// verification evidence are never saved; every restart requires a fresh Arm.
func (m *Manager) AllowTunnel(luid uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.active || !m.armed || m.state.Recovery {
		return ErrRecoveryRequired
	}
	if m.state.LogicalSessionID != "" && (!m.runtimeOwned || !m.state.CleanupRequired || m.state.Phase != "running") {
		return ErrRecoveryRequired
	}
	if luid == 0 {
		return errors.New("zero TUN LUID")
	}
	if m.policy.Tunnel != 0 && m.policy.Tunnel != luid {
		return ErrNotBlocked
	}
	if err := m.verifyTunnel(luid); err != nil {
		return err
	}
	p := m.policy
	p.Tunnel = luid
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.armed = false
		m.rebindReady = false
		m.state.Recovery = true
		return err
	}
	m.policy = p
	return nil
}

// AllowUDPRelay authorizes a parsed SOCKS UDP ASSOCIATE response for the
// current verified session, before the service opens its UDP socket. It never
// resolves names. The authenticated server may return a different public host.
// Relay permits are nonpersistent and bounded to 64 active exact IPv4 endpoints.
// Each successful call owns one reference, paired with RemoveUDPRelay after
// that association's socket closes, including failed socket construction.
func (m *Manager) AllowUDPRelay(endpoint netip.AddrPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.active || !m.armed || m.state.Recovery || m.policy.Tunnel == 0 {
		return ErrRecoveryRequired
	}
	if !m.policy.EntryPolicy.AuthenticatedSOCKSRelay {
		return errors.New("UDP relay requires SOCKS session")
	}
	if !validEndpoint(endpoint) || !gate.PublicIPv4(endpoint.Addr().String()) || isVirtual(endpoint.Addr(), m.policy.Virtual) {
		return errors.New("UDP relay must be an authenticated exact public IPv4 endpoint")
	}
	if err := m.verifyTunnel(m.policy.Tunnel); err != nil {
		return err
	}
	if references := m.udpRelayRefs[endpoint]; references != 0 {
		if references == ^uint64(0) {
			return errors.New("too many UDP relay associations")
		}
		m.udpRelayRefs[endpoint] = references + 1
		return nil
	}
	if len(m.policy.UDPRelays) >= 64 {
		return errors.New("too many UDP relay endpoints for this session")
	}
	p := m.policy
	p.UDPRelays = append(append([]netip.AddrPort(nil), p.UDPRelays...), endpoint)
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.armed = false
		m.rebindReady = false
		m.state.Recovery = true
		return fmt.Errorf("allow UDP relay: %w", err)
	}
	m.policy = p
	if m.udpRelayRefs == nil {
		m.udpRelayRefs = make(map[netip.AddrPort]uint64)
	}
	m.udpRelayRefs[endpoint] = 1
	return nil
}

// RemoveUDPRelay releases one successful AllowUDPRelay reference. The trusted
// host must bind both callbacks to the same session and serialize them with
// Block/rearm, so a late close cannot revoke a new session's shared endpoint.
// Cleanup does not require a live TUN or fresh authorization. A failed final
// removal retains the reference and recovery state; the host must Block and
// surface the error rather than claim that permission was revoked.
func (m *Manager) RemoveUDPRelay(endpoint netip.AddrPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	references := m.udpRelayRefs[endpoint]
	if references == 0 {
		return nil
	}
	if references > 1 {
		m.udpRelayRefs[endpoint] = references - 1
		return nil
	}
	p := m.policy
	p.UDPRelays = make([]netip.AddrPort, 0, len(m.policy.UDPRelays)-1)
	for _, relay := range m.policy.UDPRelays {
		if relay != endpoint {
			p.UDPRelays = append(p.UDPRelays, relay)
		}
	}
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.armed = false
		m.rebindReady = false
		m.state.Recovery = true
		return fmt.Errorf("remove UDP relay: %w", err)
	}
	m.policy = p
	delete(m.udpRelayRefs, endpoint)
	return nil
}

// Block revokes tunnel, UDP relay and direct permits atomically, retaining control/network setup
// exceptions and the owner. The caller must also close the engine business gate.
func (m *Manager) Block() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.state.ProtectIntent {
		return nil
	}
	p := m.policy
	p.Tunnel = 0
	p.LocalTunnel = 0
	if p.LocalOnly {
		p.DNS, p.ControlDNS = nil, nil
		p.Links = nil // Network refresh must not retain old on-link LAN prefixes.
	}
	p.UDPRelays = nil
	p.Direct = nil
	// A failed transaction retains the old rules, possibly including the TUN
	// permit. The engine gate must stay closed and a new session cannot proceed
	// until revocation succeeds. Do not report this as successfully blocked.
	m.armed = false
	m.rebindReady = false
	m.localPrepared = false
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.state.Recovery = true
		return err
	}
	m.policy = p
	m.udpRelayRefs = nil
	m.directRefs = nil
	m.active = true
	m.rebindReady = true
	m.armed = false
	m.dnsBootstrap = false
	return nil
}

// Release is explicit recovery: the service must first authenticate OwnerSID
// and stop its engine. Business login is not required. Unowned residue requires
// administrator Repair. Removal commits before the durable inactive tombstone.
func (m *Manager) Release() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if m.state.ProtectIntent && m.state.OwnerSID == "" {
		return ErrOwner
	}
	if m.state.CleanupRequired {
		return ErrRecoveryRequired
	}
	return m.release()
}

func (m *Manager) release() error {
	m.armed = false
	m.rebindReady = false
	if err := m.fw.Remove(); err != nil {
		m.state.Recovery = true
		return err
	}
	m.active = false
	m.armed = false
	m.rebindReady = true
	m.policy.Tunnel = 0
	m.policy.LocalTunnel, m.policy.LocalOnly = 0, false
	m.localPrepared = false
	m.policy.UDPRelays = nil
	m.udpRelayRefs = nil
	m.policy.Direct = nil
	m.directRefs = nil
	m.policy.ControlDNS = nil
	m.dnsBootstrap = false
	if err := m.saveState(intent{Version: 2}); err != nil {
		m.rebindReady = false
		m.state.Recovery = true
		return fmt.Errorf("rules removed but intent clear failed: %w", err)
	}
	m.runtimeOwned = false
	return nil
}

func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active || m.state.ProtectIntent
}
func (m *Manager) OwnerSID() string { m.mu.Lock(); defer m.mu.Unlock(); return m.state.OwnerSID }
func (m *Manager) RecoveryRequired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Recovery || m.state.CleanupRequired && (!m.runtimeOwned || m.state.Phase == "maintenance")
}

// Authorized compares the complete binding for business-session commands.
// Use OwnerSID for an authenticated owner's explicit Release without login.
func (m *Manager) Authorized(sid, account, resource string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.state.ProtectIntent && sid != "" && m.state.OwnerSID == sid && m.state.AccountKey == account && m.state.ResourceID == resource
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return errors.Join(m.fw.Close(), m.store.Close())
}

// SetVirtualPrefixes must be called before Arm/ArmDNS when the engine uses additional
// virtual pools. Default pools remain excluded. Changing pools with active
// intent is rejected; release or keep the existing pools for this session.
func (m *Manager) SetVirtualPrefixes(prefixes []netip.Prefix) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if m.state.ProtectIntent {
		return ErrNotBlocked
	}
	if len(prefixes) > 32 {
		return errors.New("too many virtual pools")
	}
	for _, p := range prefixes {
		if !p.IsValid() || p.Bits() == 0 || p.Addr().Is4In6() {
			return errors.New("invalid virtual pool")
		}
	}
	m.policy.Virtual = append(defaultVirtualPrefixes(), prefixes...)
	return nil
}

func validID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && strings.IndexFunc(s, unicode.IsControl) < 0
}
func validSID(s string) bool {
	p := strings.Split(s, "-")
	if len(p) < 4 || len(p) > 18 || p[0] != "S" || p[1] != "1" {
		return false
	}
	for i, x := range p[2:] {
		bits := 32
		if i == 0 {
			bits = 48
		}
		n, e := strconv.ParseUint(x, 10, bits)
		if e != nil || strconv.FormatUint(n, 10) != x {
			return false
		}
	}
	return true
}
func validEndpoint(p netip.AddrPort) bool {
	a := p.Addr()
	return p.IsValid() && p.Port() != 0 && !a.IsUnspecified() && !a.IsMulticast() && !a.IsLoopback() && !a.Is4In6() && a.Zone() == "" && !a.IsLinkLocalUnicast()
}
func validIntent(r intent) bool {
	if r.Version != 1 && r.Version != 2 {
		return false
	}
	if r.Version == 1 && (r.ResourceKey != (engine.ResourceKey{}) || r.LogicalSessionID != "" || r.Phase != "" || r.CleanupRequired || r.Revision != 0) {
		return false
	}
	if !r.ProtectIntent {
		return r.OwnerSID == "" && r.AccountKey == "" && r.ResourceID == "" && r.Mode == "" && r.Since == "" && !r.Recovery && r.ResourceKey == (engine.ResourceKey{}) && r.LogicalSessionID == "" && r.Phase == "" && !r.CleanupRequired
	}
	if r.Mode != "tun" {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, r.Since); e != nil {
		return false
	}
	if r.Version == 2 {
		if r.Revision == 0 {
			return false
		}
		if r.LogicalSessionID == "" {
			if r.ResourceKey != (engine.ResourceKey{}) || r.Phase != "unbound" && r.Phase != "legacy" {
				return false
			}
		} else if !validResourceKey(r.ResourceKey) || !validID(r.LogicalSessionID) || r.ResourceKey.AccountKey != r.AccountKey || r.ResourceKey.SourceID != r.ResourceID ||
			(r.Phase != "blocked" && r.Phase != "running" && r.Phase != "local" && r.Phase != "maintenance") || (r.Phase == "running" || r.Phase == "local" || r.Phase == "maintenance") && !r.CleanupRequired {
			return false
		}
	}
	if r.OwnerSID == "" {
		return r.Recovery && r.AccountKey == "" && r.ResourceID == ""
	}
	return validSID(r.OwnerSID) && validID(r.AccountKey) && validID(r.ResourceID)
}

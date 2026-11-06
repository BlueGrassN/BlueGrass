package control

import (
	"bluegrass/native/core/clock"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/gate"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

var ErrEngine = errors.New("ENGINE_FAILURE")
var ErrOwner = errors.New("OWNER_MISMATCH")

const maxHistory = 4096

type attempt struct {
	ctx     context.Context
	cancel  context.CancelFunc
	profile ConnectionProfile
}
type receipt struct {
	digest   [32]byte
	ack      CommandAck
	err      error
	sequence uint64
}

// Controller is an in-process reference, NOT an authenticated service. A trusted
// host supplies owner after authenticating the caller. No Engine ships here.
type Controller struct {
	commands        sync.Mutex // serializes commands, expiry processing and owner rebinding
	engineMu        sync.Mutex // serializes ALL engine operations, including probe
	mu              sync.Mutex // never held while waiting for engineMu
	e               engine.Engine
	clk             clock.Clock
	runtime         string
	owner           Owner
	snap            NativeSnapshot
	active          *attempt
	timer           *time.Timer
	lastSequence    uint64
	revision        uint64
	receipts        map[string]receipt
	receiptOrder    []string
	receiptNext     int
	evictedSequence uint64
	attempts        map[ControlContext]bool
	retiredOwners   map[string]bool
	counterEpochs   map[string]bool
	closed          bool
	clockInvalid    bool
	workers         sync.WaitGroup
}

func New(e engine.Engine, clk clock.Clock, runtime string, owner Owner) (*Controller, error) {
	if e == nil || clk == nil || runtime == "" || !validOwner(owner) {
		return nil, errors.New("INVALID_CONFIGURATION")
	}
	reading := clk.Read()
	if reading.RuntimeEpoch != runtime || reading.EffectiveUTC.IsZero() || reading.MonotonicNanos < 0 {
		return nil, errors.New("INVALID_CLOCK")
	}
	c := &Controller{e: e, clk: clk, runtime: runtime, owner: owner, receipts: map[string]receipt{}, attempts: map[ControlContext]bool{}, retiredOwners: map[string]bool{}, counterEpochs: map[string]bool{}}
	c.snap = NativeSnapshot{RuntimeEpoch: runtime, Connection: "disconnected", ActualMode: "none", Protection: "not_implemented", Time: reading}
	return c, nil
}
func validOwner(o Owner) bool {
	return o.Environment != "" && o.DataSource != "" && o.AccountKey != "" && o.ClientEpoch != ""
}
func validKey(k ResourceKey) bool {
	return k.Environment != "" && k.DataSource != "" && k.AccountKey != "" && k.SourceID != "" && (k.Kind == "subscription" || k.Kind == "asset")
}
func (c *Controller) owns(x ControlContext) bool {
	k := x.ResourceKey
	return x.ClientEpoch == c.owner.ClientEpoch && k.Environment == c.owner.Environment && k.DataSource == c.owner.DataSource && k.AccountKey == c.owner.AccountKey
}

// Apply acknowledges Connect preparation asynchronously; only a subsequent
// connected snapshot confirms prepared forwarding, independently of the optional
// post-connect exit observation. The last maxHistory evaluated
// requests retain their ORIGINAL receipts (including errors); a retained ID with
// different contents is a conflict. Once evicted, that sequence and all older
// sequences are stale, never replayed. Request IDs must be unique within an owner
// epoch; ID conflicts are detectable only within the retained receipt window.
// IPC request cancellation after acceptance does not cancel the session.
func (c *Controller) Apply(ctx context.Context, req ControlRequest) (CommandAck, error) {
	c.commands.Lock()
	defer c.commands.Unlock()
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		a := c.ack(req, "rejected", "CANCELLED")
		c.mu.Unlock()
		return a, err
	}
	if c.closed {
		a := c.ack(req, "rejected", "CLOSED")
		c.mu.Unlock()
		return a, errors.New("CLOSED")
	}
	if req.Context.SchemaVersion != 1 {
		a := c.ack(req, "rejected", "SYS-003")
		c.mu.Unlock()
		return a, nil
	}
	handshake := req.Command == Hello || req.Command == GetStatus
	if req.Context.ClientEpoch != c.owner.ClientEpoch || !handshake && !c.owns(req.Context) || req.Context.RuntimeEpoch != "" && req.Context.RuntimeEpoch != c.runtime {
		a := c.ack(req, "stale", "STALE_CONTEXT")
		c.mu.Unlock()
		return a, nil
	}
	if req.RequestID == "" || len(req.RequestID) > 128 || req.Sequence == 0 {
		a := c.ack(req, "rejected", "INVALID_REQUEST")
		c.mu.Unlock()
		return a, nil
	}
	raw, err := json.Marshal(req)
	if err != nil {
		a := c.ack(req, "rejected", "INVALID_REQUEST")
		c.mu.Unlock()
		return a, nil
	}
	digest := sha256.Sum256(raw)
	if prior, ok := c.receipts[req.RequestID]; ok {
		if digest != prior.digest {
			a := c.ack(req, "rejected", "REQUEST_CONFLICT")
			c.mu.Unlock()
			return a, nil
		}
		a := cloneAck(prior.ack)
		c.mu.Unlock()
		return a, prior.err
	}
	// Detach caller-owned pointer fields; receipts keep only a digest, no secrets.
	var detached ControlRequest
	if json.Unmarshal(raw, &detached) != nil {
		a := c.ack(req, "rejected", "INVALID_REQUEST")
		c.mu.Unlock()
		return a, nil
	}
	req = detached
	if req.Sequence <= c.lastSequence || req.Sequence <= c.evictedSequence {
		a := c.ack(req, "stale", "STALE_SEQUENCE")
		c.mu.Unlock()
		return a, nil
	}
	disposition, code := c.validate(req)
	if disposition != "" {
		a := c.ack(req, disposition, code)
		c.remember(req, digest, a, nil)
		c.mu.Unlock()
		return a, nil
	}
	c.lastSequence = req.Sequence
	if handshake {
		a := c.ack(req, "applied", "")
		c.remember(req, digest, a, nil)
		c.mu.Unlock()
		return a, nil
	}
	var start *attempt
	var stop, release bool
	switch req.Command {
	case Connect:
		stop = c.snap.Context != nil
		c.cancelLocked()
		actx, cancel := context.WithCancel(context.Background())
		start = &attempt{actx, cancel, *req.Params.Profile}
		c.active = start
		c.attempts[req.Context] = true
		c.revision = start.profile.ConfigVersion
		x := req.Context
		c.snap.Context = &x
		c.snap.Connection = "starting"
		// Starting/switching may change engine state. The requested mode is not
		// an observation; only the current attempt's successful Prepare proves it.
		c.snap.ActualMode = "unknown"
		c.snap.ProtectionIntent = true
		c.snap.Protection = "intent_blocking"
		c.snap.Expired = false
		c.snap.ErrorCode = ""
		c.snap.ValidUntilUTC = cloneTime(start.profile.ValidUntilUTC)
		c.snap.Traffic = TrafficSnapshot{}
		c.counterEpochs = map[string]bool{}
	case Pause, Disconnect, ReleaseProtection:
		c.cancelLocked()
		stop = true
		release = req.Command != Pause
		c.snap.Connection = "stopping"
	case ApplyResourceUpdate:
		u := req.Params.Update
		now := c.readLocked().EffectiveUTC
		// A queued timer may lose the command lock to a late refresh. The OLD
		// deadline must invalidate the attempt before accepting any extension.
		if c.active != nil && (c.clockInvalid || gate.Expired(now, c.snap.ValidUntilUTC)) {
			c.expireLocked()
			stop = true
		}
		c.revision = u.Revision
		c.snap.ValidUntilUTC = cloneTime(u.ValidUntilUTC)
		c.snap.Expired = gate.Expired(now, u.ValidUntilUTC)
		if c.active != nil {
			c.active.profile.ValidUntilUTC = cloneTime(u.ValidUntilUTC)
		}
		if u.Unavailable || u.Profile != nil || c.snap.Expired {
			c.cancelLocked()
			stop = true
			c.snap.Connection = "stopping"
		}
	}
	c.snap.EventSequence++
	if start == nil && !stop {
		c.scheduleLocked()
		a := c.ack(req, "applied", "")
		c.remember(req, digest, a, nil)
		c.mu.Unlock()
		return a, nil
	}
	c.mu.Unlock()
	// Invalidation precedes waiting: even a noncooperative late probe cannot open.
	c.engineMu.Lock()
	c.mu.Lock()
	err = c.closeGateLocked(stop)
	if err != nil {
		c.failLocked("ENGINE_FAILURE")
	} else if start != nil {
		c.scheduleLocked()
	} else if release {
		c.snap.Connection = "disconnected"
		c.snap.ProtectionIntent = false
		c.snap.Protection = "not_implemented"
		c.snap.ActualMode = "none"
		c.snap.Traffic.Available = false
	} else {
		c.snap.Connection = "paused"
		c.snap.Protection = "intent_blocking"
		c.snap.Traffic.Available = false
	}
	a := c.ack(req, "applied", "")
	if err != nil {
		a = c.ack(req, "rejected", "ENGINE_FAILURE")
	}
	c.remember(req, digest, a, err)
	if start != nil && err == nil {
		c.workers.Add(1)
		go c.run(start)
	}
	c.mu.Unlock()
	c.engineMu.Unlock()
	return a, err
}
func (c *Controller) validate(req ControlRequest) (string, string) {
	x := req.Context
	if req.Command == Hello || req.Command == GetStatus {
		if req.Params.Profile != nil || req.Params.Update != nil {
			return "rejected", "INVALID_PARAMETERS"
		}
		return "", ""
	}
	if x.RuntimeEpoch != c.runtime || x.LogicalSessionID == "" || x.AttemptID == "" || !validKey(x.ResourceKey) {
		return "rejected", "INVALID_CONTEXT"
	}
	if req.Command == Connect {
		if req.Params.Profile == nil || req.Params.Update != nil {
			return "rejected", "INVALID_PARAMETERS"
		}
		if c.attempts[x] {
			return "stale", "RETIRED_ATTEMPT"
		}
		if len(c.attempts) >= maxHistory {
			return "rejected", "HISTORY_FULL"
		}
		for seen := range c.attempts {
			if seen.LogicalSessionID == x.LogicalSessionID && seen.ResourceKey != x.ResourceKey {
				return "rejected", "SESSION_RESOURCE_MISMATCH"
			}
		}
		p := req.Params.Profile
		if p.Key != x.ResourceKey {
			return "rejected", "PROFILE_CONTEXT_MISMATCH"
		}
		now := c.readLocked().EffectiveUTC
		if c.clockInvalid {
			return "rejected", "INVALID_CLOCK"
		}
		if code := ValidateProfile(*p, now); code != "" {
			return "rejected", code
		}
		return "", ""
	}
	if c.snap.Context == nil || *c.snap.Context != x {
		return "stale", "STALE_CONTEXT"
	}
	switch req.Command {
	case Pause, Disconnect, ReleaseProtection:
		if req.Params.Profile != nil || req.Params.Update != nil {
			return "rejected", "INVALID_PARAMETERS"
		}
	case ApplyResourceUpdate:
		u := req.Params.Update
		if u == nil || req.Params.Profile != nil {
			return "rejected", "INVALID_PARAMETERS"
		}
		if u.Revision <= c.revision {
			return "stale", "STALE_RESOURCE_REVISION"
		}
		if u.Profile != nil && u.Profile.Key != x.ResourceKey {
			return "rejected", "PROFILE_CONTEXT_MISMATCH"
		}
	default:
		return "rejected", "UNSUPPORTED_COMMAND"
	}
	return "", ""
}
func ValidateProfile(p ConnectionProfile, now time.Time) string {
	if gate.Expired(now, p.ValidUntilUTC) {
		return "ELIG-003"
	}
	if p.ProtocolConfig == nil && p.Protocol != "socks5" {
		return "ELIG-007"
	}
	if _, err := p.EffectiveProtocol(); err != nil {
		return "ELIG-005"
	}
	if !validKey(p.Key) || p.Entry.Port < 1 || p.Entry.Port > 65535 || !validHost(p.Entry.Host) || p.ConfigVersion == 0 || p.ConfigFingerprint == "" {
		return "ELIG-005"
	}
	if p.Mode != "system_proxy" && p.Mode != "tun" && p.Mode != "vpn" {
		return "INVALID_MODE"
	}
	if p.RoutingMode != "" && p.RoutingMode != "global" && p.RoutingMode != "bypass_cn" {
		return "INVALID_MODE"
	}
	if len(p.Auth.Username) > 1024 || len(p.Auth.Password) > 4096 {
		return "ELIG-005"
	}
	return ""
}
func validHost(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.TrimSpace(host) != host {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, v := range label {
			if !(v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || v == '-') {
				return false
			}
		}
	}
	return true
}

func (c *Controller) run(a *attempt) {
	defer c.workers.Done()
	c.engineMu.Lock()
	defer c.engineMu.Unlock()
	c.mu.Lock()
	if !c.currentLocked(a) {
		c.mu.Unlock()
		return
	}
	p := a.profile
	c.mu.Unlock()
	err := c.e.Prepare(a.ctx, p)
	c.mu.Lock()
	if !c.currentLocked(a) {
		c.mu.Unlock()
		return
	}
	if err != nil {
		c.failLocked("ENGINE_FAILURE")
		_ = c.closeGateLocked(true)
		c.mu.Unlock()
		return
	}
	c.snap.ActualMode = p.Mode
	reading := c.readLocked()
	if c.clockInvalid {
		c.failLocked("INVALID_CLOCK")
		_ = c.closeGateLocked(true)
		c.mu.Unlock()
		return
	}
	if gate.Expired(reading.EffectiveUTC, c.snap.ValidUntilUTC) {
		c.expireLocked()
		_ = c.closeGateLocked(true)
		c.mu.Unlock()
		return
	}
	// Preparation/current-attempt/expiry validation and gate-open commit are
	// indivisible relative to cancellation. Exit observation is not a gate.
	if c.e.SetBusinessGate(true) != nil {
		c.snap.GateKnown = false
		c.failLocked("ENGINE_FAILURE")
		_ = c.closeGateLocked(true)
		c.mu.Unlock()
		return
	}
	// Applying an OS gate can take time. Do not commit a connected claim or
	// start a probe if the lease elapsed during that synchronous operation.
	reading = c.readLocked()
	if c.clockInvalid || gate.Expired(reading.EffectiveUTC, c.snap.ValidUntilUTC) {
		if c.clockInvalid {
			c.failLocked("INVALID_CLOCK")
		} else {
			c.expireLocked()
		}
		_ = c.closeGateLocked(true)
		c.mu.Unlock()
		return
	}
	c.snap.BusinessGateOpen = true
	c.snap.GateKnown = true
	c.snap.Connection = "connected"
	c.snap.Protection = "intent_forwarding"
	c.snap.EventSequence++
	c.mu.Unlock()

	result, err := c.e.ProbeExit(a.ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(a) {
		return
	}
	reading = c.readLocked()
	if c.clockInvalid {
		c.failLocked("INVALID_CLOCK")
		_ = c.closeGateLocked(true)
		return
	}
	if gate.Expired(reading.EffectiveUTC, c.snap.ValidUntilUTC) {
		c.expireLocked()
		_ = c.closeGateLocked(true)
		return
	}
	// A failed/untrusted/malformed probe leaves forwarding connected with an
	// unknown exit. Only this current attempt can publish a public IPv4. A
	// diagnostic may already have published a newer observation while this
	// initial probe was pending; never overwrite that accepted observation.
	if c.snap.VerifiedExitIPv4 == "" && err == nil && result.Authenticated && result.Reachable && gate.PublicIPv4(result.IPv4) {
		c.snap.VerifiedExitIPv4 = result.IPv4
		c.snap.EventSequence++
	}
}
func (c *Controller) currentLocked(a *attempt) bool {
	// Each accepted Connect allocates a unique attempt; every owner/session
	// transition first cancels and clears it. A second copied wire context adds
	// no identity protection and can drift from the authoritative snapshot.
	return c.active == a && a.ctx.Err() == nil
}
func (c *Controller) cancelLocked() {
	c.snap.VerifiedExitIPv4 = ""
	if c.active != nil {
		c.active.cancel()
		c.active = nil
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}
func (c *Controller) failLocked(code string) {
	c.cancelLocked()
	c.snap.Connection = "failed"
	c.snap.ErrorCode = code
	c.snap.Protection = "intent_blocking"
	c.snap.Traffic.Available = false
	c.snap.EventSequence++
}

// Caller holds engineMu and mu. On gate error, still attempt Stop; do not claim
// physical closure. GateKnown=false explicitly reports uncertainty.
func (c *Controller) closeGateLocked(stop bool) error {
	err := c.e.SetBusinessGate(false)
	c.snap.GateKnown = err == nil
	c.snap.BusinessGateOpen = false
	if stop {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stopErr := c.e.Stop(ctx)
		cancel()
		if stopErr != nil {
			err = ErrEngine
		}
	}
	if err != nil {
		return ErrEngine
	}
	return nil
}
func (c *Controller) expireLocked() {
	c.cancelLocked()
	c.snap.Expired = true
	c.snap.Connection = "paused"
	c.snap.Protection = "intent_blocking"
	c.snap.Traffic.Available = false
	c.snap.EventSequence++
}
func (c *Controller) scheduleLocked() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if c.active == nil || c.snap.ValidUntilUTC == nil {
		return
	}
	d := c.snap.ValidUntilUTC.Sub(c.readLocked().EffectiveUTC)
	if d < 0 {
		d = 0
	}
	c.timer = time.AfterFunc(d, func() { _ = c.CheckExpiry() })
}

// CheckExpiry is also the host wake/resume hook; timers run independently of UI.
func (c *Controller) CheckExpiry() error {
	c.commands.Lock()
	defer c.commands.Unlock()
	c.mu.Lock()
	if c.closed || c.active == nil {
		c.mu.Unlock()
		return nil
	}
	now := c.readLocked().EffectiveUTC
	if !c.clockInvalid && !gate.Expired(now, c.snap.ValidUntilUTC) {
		c.scheduleLocked()
		c.mu.Unlock()
		return nil
	}
	if c.clockInvalid {
		c.failLocked("INVALID_CLOCK")
	} else {
		c.expireLocked()
	}
	c.mu.Unlock()
	c.engineMu.Lock()
	defer c.engineMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.closeGateLocked(true)
	if err != nil {
		c.failLocked("ENGINE_FAILURE")
	}
	return err
}

// RebindOwner is a trusted-host operation, not a wire command. It requires
// external same-account authentication and a new attempt to resume forwarding.
func (c *Controller) RebindOwner(ctx context.Context, expected, next Owner) error {
	c.commands.Lock()
	defer c.commands.Unlock()
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.closed || expected != c.owner || !validOwner(next) || next.Environment != expected.Environment || next.DataSource != expected.DataSource || next.AccountKey != expected.AccountKey || next.ClientEpoch == expected.ClientEpoch || c.retiredOwners[next.ClientEpoch] {
		c.mu.Unlock()
		return ErrOwner
	}
	if len(c.retiredOwners) >= maxHistory {
		c.mu.Unlock()
		return errors.New("HISTORY_FULL")
	}
	c.cancelLocked()
	hasSession := c.snap.Context != nil
	c.mu.Unlock()
	c.engineMu.Lock()
	defer c.engineMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if hasSession {
		if err := c.closeGateLocked(true); err != nil {
			c.failLocked("ENGINE_FAILURE")
			return err
		}
		c.snap.Context.ClientEpoch = next.ClientEpoch
		c.snap.Connection = "paused"
		c.snap.Protection = "intent_blocking"
		c.snap.Traffic = TrafficSnapshot{}
	}
	c.retiredOwners[expected.ClientEpoch] = true
	c.owner = next
	c.receipts = map[string]receipt{}
	c.receiptOrder = nil
	c.receiptNext = 0
	c.evictedSequence = 0
	c.lastSequence = 0
	c.snap.EventSequence++
	return nil
}

// Close stops owned work while retaining protection intent. It is not Disconnect.
func (c *Controller) Close() error {
	c.commands.Lock()
	defer c.commands.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.cancelLocked()
	hasSession := c.snap.Context != nil
	c.mu.Unlock()
	c.engineMu.Lock()
	c.mu.Lock()
	var err error
	if hasSession {
		err = c.closeGateLocked(true)
		c.snap.Connection = "paused"
		c.snap.Protection = "intent_blocking"
		c.snap.Traffic.Available = false
		if err != nil {
			c.failLocked("ENGINE_FAILURE")
		}
	}
	c.mu.Unlock()
	c.engineMu.Unlock()
	c.workers.Wait()
	return err
}

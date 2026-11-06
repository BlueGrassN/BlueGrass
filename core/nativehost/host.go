// Package nativehost is a private inherited-pipe host. It exposes no control socket
// and optionally manages the current user's system proxy. It installs no service.
package nativehost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"time"

	"bluegrass/native/core/clock"
	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/localengine"
	"bluegrass/native/core/winproxy"
)

type Request struct {
	ID            string                  `json:"id"`
	Command       string                  `json:"command"`
	Host          string                  `json:"host,omitempty"`
	Profile       json.RawMessage         `json:"profile,omitempty"`
	Mode          string                  `json:"mode,omitempty"`
	AllowLan      *bool                   `json:"allowLan,omitempty"`
	EntryDNS      *entrydns.Policy        `json:"entryDns,omitempty"`
	ServerTimeUTC *time.Time              `json:"serverTimeUtc,omitempty"`
	Update        json.RawMessage         `json:"update,omitempty"`
	Context       *control.ControlContext `json:"context,omitempty"`
}
type Response struct {
	ID           string         `json:"id"`
	OK           bool           `json:"ok"`
	Error        string         `json:"error,omitempty"`
	Snapshot     map[string]any `json:"snapshot,omitempty"`
	RuntimeEpoch string         `json:"runtimeEpoch,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
}
type Host struct {
	engine                   NetworkEngine
	tunnel                   TunnelEngine
	controller               *control.Controller
	runtime, client          string
	clock                    *clock.Monotonic
	context                  control.ControlContext
	owner                    control.Owner
	sequence, sample         uint64
	proxy                    proxyManager
	proxyEngine              *proxyEngine
	proxyRecoveryRequired    bool
	proxySettingsPreserved   bool
	networkChanged           bool
	baseUpload, baseDownload uint64
	trafficIncomplete        bool
	diagnostic               *diagnosticJob
	diagnosticGeneration     uint64
	diagnosticWorker         <-chan struct{}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(b[:])
}

const localProxyPort uint16 = 12808

func New(port uint16) (*Host, error) {
	local, err := localengine.New(port)
	if err != nil {
		return nil, err
	}
	epoch := randomID()
	return &Host{engine: local, runtime: epoch, client: randomID(), clock: clock.New(epoch, time.Now().UTC(), clock.Local)}, nil
}
func (h *Host) Close() error {
	h.cancelDiagnostic()
	var err error
	if h.controller != nil {
		err = h.controller.Close()
	}
	if h.proxy != nil {
		err = errors.Join(err, h.restoreProxy(false))
	}
	return errors.Join(err, h.engine.Close())
}

func (h *Host) restoreProxy(releaseExternal bool) error {
	err := h.proxy.Restore()
	if releaseExternal && errors.Is(err, winproxy.ErrOwnershipLost) {
		err = h.proxy.Relinquish()
		if err == nil {
			h.proxySettingsPreserved = true
		}
	}
	h.proxyRecoveryRequired = err != nil
	return err
}

func (h *Host) capabilities() []string {
	values := []string{"local_http_proxy", "socks5", "exit_ipv4_verification"}
	if h.tunnel != nil {
		return []string{"tun", "socks5", "exit_ipv4_verification", "persistent_protection"}
	}
	if h.proxy != nil {
		values = append(values, "system_proxy")
	}
	return values
}

// Only explicit status requests produce samples. Other replies retain the last
// accepted sample so control-plane traffic cannot create gaps for the UI.
func (h *Host) sampleTraffic() {
	if h.controller == nil {
		return
	}
	s := h.controller.Snapshot()
	if s.Connection != "connected" || s.Context == nil {
		return
	}
	up, down := h.engine.Counters()
	up = h.addCounter(h.baseUpload, up)
	down = h.addCounter(h.baseDownload, down)
	if h.controller.AcceptTraffic(control.TrafficSnapshot{Context: *s.Context, CounterEpoch: s.Context.LogicalSessionID,
		Sequence: h.sample + 1, MonotonicNanos: s.Time.MonotonicNanos, UploadBytes: strconv.FormatUint(up, 10),
		DownloadBytes: strconv.FormatUint(down, 10), Available: true, Complete: !h.trafficIncomplete}) {
		h.sample++
	}
}

func (h *Host) snapshot() map[string]any {
	// Some platform managers close forwarding independently when ownership or
	// the physical network changes. Synchronize the controller before publishing
	// a reply; cached connection/exit claims cannot outlive that closed gate.
	if monitor, ok := h.engine.(interface{ NetworkChanged() bool }); ok && monitor.NetworkChanged() {
		_ = h.NetworkChanged()
	}
	h.collectDiagnostic()
	var s control.NativeSnapshot
	if h.controller != nil {
		s = h.controller.Snapshot()
	}
	applied := false
	if h.proxy != nil {
		var err error
		applied, err = h.proxy.State()
		if err != nil {
			h.proxyRecoveryRequired = true
			// A competing writer or an unreadable record cannot retain a
			// connected claim. Leave their settings intact and stop forwarding.
			if h.controller != nil {
				_ = h.pause()
				s = h.controller.Snapshot()
			}
		}
	}
	result := map[string]any{"connection": "disconnected", "actualMode": "none", "businessGateOpen": false,
		"protectionIntent":   false,
		"systemProxyApplied": applied, "proxyRecoveryRequired": h.proxyRecoveryRequired,
		"proxySettingsPreserved": h.proxySettingsPreserved,
		"gateKnown":              true, "proxyAddress": h.engine.Address(), "runtimeEpoch": h.runtime,
		"capabilities": h.capabilities()}
	if diagnostic := h.diagnosticEnvelope(); diagnostic != nil {
		result["diagnostic"] = diagnostic
	}
	if h.controller == nil {
		if h.tunnel != nil && h.tunnel.Active() {
			result["connection"] = "paused"
			result["actualMode"] = "tun"
			result["protectionIntent"] = true
			result["protection"] = h.tunnelProtectionState(false)
		}
		return result
	}
	data, _ := json.Marshal(s)
	_ = json.Unmarshal(data, &result)
	result["systemProxyApplied"] = applied
	result["proxyAddress"] = h.engine.Address()
	// Never publish an engine cache directly: only the controller can bind a
	// completed observation to the still-current, connected attempt.
	delete(result, "verifiedExitIpv4")
	if s.Connection == "connected" && s.GateKnown && s.BusinessGateOpen && gate.PublicIPv4(s.VerifiedExitIPv4) {
		result["verifiedExitIpv4"] = s.VerifiedExitIPv4
	}
	if h.networkChanged {
		result["errorCode"] = "NETWORK_CHANGED"
	}
	if s.ActualMode == "system_proxy" && h.proxy == nil {
		result["actualMode"] = "local_proxy"
	}
	if h.tunnel != nil {
		result["protectionIntent"] = h.tunnel.Active()
		if s.BusinessGateOpen && h.tunnel.Active() {
			result["protection"] = h.tunnelProtectionState(true)
		} else {
			result["protection"] = h.tunnelProtectionState(false)
		}
	} else if s.BusinessGateOpen {
		result["protection"] = "local_gate_forwarding"
	} else {
		result["protection"] = "local_gate_blocking"
	}
	return result
}

func (h *Host) pause() error {
	h.cancelDiagnostic()
	if h.controller == nil || h.context.AttemptID == "" {
		return nil
	}
	h.sequence++
	_, err := h.controller.Apply(context.Background(), control.ControlRequest{Context: h.context,
		RequestID: randomID(), Sequence: h.sequence, Command: control.Pause})
	return err
}
func (h *Host) Handle(r Request) Response {
	reject := func(code string) Response { return Response{ID: r.ID, OK: false, Error: code, Snapshot: h.snapshot()} }
	if r.ID == "" || len(r.ID) > 128 {
		return reject("INVALID_REQUEST")
	}
	connecting := r.Command == "Connect" || r.Command == "SwitchResource"
	if !connecting && (len(r.Profile) != 0 || r.Mode != "" || r.AllowLan != nil || r.EntryDNS != nil) {
		return reject("INVALID_PARAMETERS")
	}
	if r.ServerTimeUTC != nil && !connecting && r.Command != "ApplyResourceUpdate" {
		return reject("INVALID_PARAMETERS")
	}
	if r.Command != "ApplyResourceUpdate" && len(r.Update) != 0 {
		return reject("INVALID_PARAMETERS")
	}
	if r.Command != "ResolveControlHost" && r.Host != "" {
		return reject("INVALID_PARAMETERS")
	}
	if r.Command != "Diagnose" && r.Command != "SwitchResource" && r.Context != nil {
		return reject("INVALID_PARAMETERS")
	}
	switch r.Command {
	case "Diagnose":
		if code := h.startDiagnostic(r); code != "" {
			return reject(code)
		}
		return Response{ID: r.ID, OK: true, Snapshot: h.snapshot()}
	case "Hello", "GetStatus":
		if r.Command == "GetStatus" {
			h.sampleTraffic()
		}
		return Response{ID: r.ID, OK: true, Snapshot: h.snapshot(), RuntimeEpoch: h.runtime,
			Capabilities: h.capabilities()}
	case "ResolveControlHost":
		// Only a trusted engine implementing the fixed control-host allowlist
		// may resolve here. Never fall back to entry DNS or the system resolver.
		resolver, ok := h.engine.(interface {
			ResolveControlHost(context.Context, string) ([]netip.Addr, error)
		})
		if !ok || r.Host == "" || len(r.Host) > 253 {
			return reject("CONTROL_DNS_FAILED")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addresses, err := resolver.ResolveControlHost(ctx, r.Host)
		cancel()
		if err != nil || len(addresses) == 0 || len(addresses) > 8 {
			return reject("CONTROL_DNS_FAILED")
		}
		values := make([]string, len(addresses))
		for i, address := range addresses {
			if !address.Is4() || !gate.PublicIPv4(address.String()) {
				return reject("CONTROL_DNS_FAILED")
			}
			values[i] = address.String()
		}
		snapshot := h.snapshot()
		snapshot["controlAddresses"] = values
		return Response{ID: r.ID, OK: true, Snapshot: snapshot}
	case "Connect", "SwitchResource":
		switching := r.Command == "SwitchResource"
		p, err := decodeProfile(r.Profile)
		if err != nil {
			return reject("INVALID_CONFIGURATION")
		}
		mode := "system_proxy"
		if h.tunnel != nil {
			mode = "tun"
		}
		if p.Mode != mode || r.Mode != "" && r.Mode != p.Mode {
			return reject("UNSUPPORTED_COMMAND")
		}
		policy := entrydns.Policy{Mode: "auto"}
		if r.EntryDNS != nil {
			policy = *r.EntryDNS
		}
		if policy.Validate() != nil {
			return reject("INVALID_CONFIGURATION")
		}
		if policy.Mode == "" {
			policy.Mode = "auto"
		}
		setter, supportsDNS := h.engine.(interface{ SetEntryDNS(entrydns.Policy) error })
		if !supportsDNS && policy.Mode == "custom" {
			return reject("UNSUPPORTED_COMMAND")
		}
		var switcher logicalSessionSwitcher
		var previousContext control.ControlContext
		if switching {
			var code string
			switcher, code = h.validateResourceSwitch(r, p)
			if code != "" {
				return reject(code)
			}
			previousContext = h.context
		} else if h.controller != nil && p.Key != h.context.ResourceKey && h.controller.Snapshot().ProtectionIntent {
			return reject("DISCONNECT_REQUIRED")
		}
		if r.ServerTimeUTC != nil && h.clock.Calibrate(*r.ServerTimeUTC) != nil {
			return reject("INVALID_CLOCK")
		}
		if h.controller != nil && h.controller.CheckExpiry() != nil {
			return reject("ENGINE_FAILURE")
		}
		if code := control.ValidateProfile(p, h.clock.Read().EffectiveUTC); code != "" {
			return reject(code)
		}
		var logicalID string
		var resumed bool
		var journal logicalSessionJournal
		if switching {
			// Cancel old work and synchronously close its gate before changing
			// preferences or a durable binding. Pause retains OS protection.
			if err := h.pause(); err != nil {
				return reject("ENGINE_FAILURE")
			}
			logicalID = randomID()
		} else {
			var code string
			logicalID, resumed, journal, code = h.selectLogicalSession(p.Key)
			if code != "" {
				return reject(code)
			}
		}
		if h.tunnel != nil {
			if err := h.tunnel.SetAllowLAN(r.AllowLan == nil || *r.AllowLan); err != nil {
				return reject("DISCONNECT_REQUIRED")
			}
		}
		if h.proxy != nil {
			// A resource switch may reuse an owned transaction, but cannot change
			// its listener/bypass or overwrite the original settings snapshot.
			active, stateErr := h.proxy.State()
			if stateErr != nil || h.proxyRecoveryRequired || h.proxyEngine == nil {
				return reject("RECOVERY_REQUIRED")
			}
			if active {
				if !switching && !h.canRetryOwnedProxy(p.Key) {
					return reject("DISCONNECT_REQUIRED")
				}
				h.proxyEngine.mu.Lock()
				matches := r.AllowLan == nil || proxyBypass(*r.AllowLan) == h.proxyEngine.bypass
				h.proxyEngine.mu.Unlock()
				if !matches {
					return reject("DISCONNECT_REQUIRED")
				}
			} else {
				allowLan := r.AllowLan == nil || *r.AllowLan
				h.proxyEngine.mu.Lock()
				h.proxyEngine.bypass = proxyBypass(allowLan)
				h.proxyEngine.mu.Unlock()
			}
		}
		controlContext := control.ControlContext{SchemaVersion: 1, ClientEpoch: h.client, RuntimeEpoch: h.runtime,
			LogicalSessionID: logicalID, AttemptID: randomID(), ResourceKey: p.Key}
		h.sequence++
		wire, _ := json.Marshal(map[string]any{"context": controlContext, "requestId": r.ID, "sequence": h.sequence,
			"command": "Connect", "params": map[string]any{"profile": r.Profile}})
		request, err := control.DecodeRequest(wire)
		if err != nil {
			return reject("INVALID_REQUEST")
		}
		h.networkChanged = false
		h.cancelDiagnostic()
		h.diagnostic = nil
		if supportsDNS {
			policy.Candidates = append([]entrydns.Candidate(nil), policy.Candidates...)
			if setter.SetEntryDNS(policy) != nil {
				return reject("DISCONNECT_REQUIRED")
			}
		}
		journalSwitched := false
		if switcher != nil {
			if err := switcher.SwitchLogicalSession(h.context.ResourceKey, p.Key, logicalID); err != nil {
				return reject(logicalSessionError(err))
			}
			journalSwitched = true
		} else if journal != nil {
			if err := journal.BindLogicalSession(p.Key, logicalID); err != nil {
				return reject(logicalSessionError(err))
			}
		}
		if controlContext.LogicalSessionID != h.context.LogicalSessionID {
			h.baseUpload = 0
			h.baseDownload = 0
			// Identity survives a service restart; cumulative byte totals do not.
			h.trafficIncomplete = resumed
		} else if h.proxy != nil {
			// A retained proxy session obtains a fresh engine whose counters
			// reset. Do not claim complete logical-session totals after retry.
			h.trafficIncomplete = true
		}
		owner := control.Owner{Environment: p.Key.Environment, DataSource: p.Key.DataSource, AccountKey: p.Key.AccountKey, ClientEpoch: h.client}
		if h.controller == nil || owner != h.owner {
			if h.controller != nil {
				if err = h.controller.Close(); err != nil {
					return reject("ENGINE_FAILURE")
				}
			}
			var network engine.Engine = h.engine
			if h.proxyEngine != nil {
				network = h.proxyEngine
			}
			h.controller, err = control.New(network, h.clock, h.runtime, owner)
			if err != nil {
				return reject("INVALID_CONFIGURATION")
			}
			h.owner = owner
		}
		ack, err := h.controller.Apply(context.Background(), request)
		if ack.Snapshot.Context != nil {
			h.context = *ack.Snapshot.Context
		}
		if err != nil || ack.Disposition != "applied" {
			code := ack.ErrorCode
			if code == "" {
				code = "ENGINE_FAILURE"
			}
			if journalSwitched && h.context != controlContext {
				// The durable destination committed, but eligibility/history may
				// reject dispatch. The old controller was already paused, with its
				// work joined and timer cancelled. Discard that obsolete context so
				// a fresh Connect can resume the committed journal without Release.
				h.controller = nil
				h.context = control.ControlContext{}
				h.owner = control.Owner{}
				reply := reject(code)
				if retainedSwitchProtection(reply.Snapshot, h.runtime) {
					// Reply-local evidence for the pending failed switch only. It is
					// never retained in Host or emitted by later status snapshots.
					reply.Snapshot["contextRecovery"] = switchContextRecovery{
						Version: 1, Reason: "resource_switch_committed", RequestID: r.ID,
						PreviousContext: previousContext, ResourceKey: p.Key,
					}
				}
				return reply
			}
			return reject(code)
		}
		return Response{ID: r.ID, OK: true, Snapshot: h.snapshot()}
	case "ApplyResourceUpdate":
		if h.controller == nil || h.context.AttemptID == "" || len(r.Update) == 0 {
			return reject("INVALID_CONTEXT")
		}
		h.sequence++
		wire, _ := json.Marshal(map[string]any{"context": h.context, "requestId": r.ID, "sequence": h.sequence, "command": r.Command, "params": map[string]any{"update": r.Update}})
		request, err := control.DecodeRequest(wire)
		if err != nil {
			return reject("INVALID_REQUEST")
		}
		if request.Params.Update.Profile != nil {
			profile, _ := json.Marshal(request.Params.Update.Profile)
			if _, err := decodeProfile(profile); err != nil {
				return reject("INVALID_CONFIGURATION")
			}
		}
		if r.ServerTimeUTC != nil {
			if h.clock.Calibrate(*r.ServerTimeUTC) != nil {
				return reject("INVALID_CLOCK")
			}
			// The authenticated time sample is useful even if this resource
			// revision is stale. Enforce the OLD deadline before applying an
			// extension; a renewed resource cannot revive its expired attempt.
			if h.controller.CheckExpiry() != nil {
				return reject("ENGINE_FAILURE")
			}
		}
		ack, err := h.controller.Apply(context.Background(), request)
		if err != nil || ack.Disposition != "applied" {
			if ack.ErrorCode != "" {
				return reject(ack.ErrorCode)
			}
			return reject("ENGINE_FAILURE")
		}
		return Response{ID: r.ID, OK: true, Snapshot: h.snapshot()}
	case "Pause", "Disconnect", "ReleaseProtection":
		h.cancelDiagnostic()
		if h.controller == nil || h.context.AttemptID == "" {
			if r.Command != "Pause" && h.tunnel != nil && h.tunnel.Release() != nil {
				return reject("RECOVERY_REQUIRED")
			}
			if r.Command != "Pause" && h.proxy != nil && h.restoreProxy(true) != nil {
				return reject("RECOVERY_REQUIRED")
			}
			return Response{ID: r.ID, OK: true, Snapshot: h.snapshot()}
		}
		h.sequence++
		request := control.ControlRequest{Context: h.context, RequestID: r.ID, Sequence: h.sequence, Command: control.Command(r.Command)}
		ack, err := h.controller.Apply(context.Background(), request)
		if err != nil || ack.Disposition != "applied" {
			code := ack.ErrorCode
			if code == "" {
				code = "ENGINE_FAILURE"
			}
			return reject(code)
		}
		if r.Command != "Pause" && h.proxy != nil && h.restoreProxy(true) != nil {
			return reject("RECOVERY_REQUIRED")
		}
		if r.Command != "Pause" && h.tunnel != nil && h.tunnel.Release() != nil {
			return reject("RECOVERY_REQUIRED")
		}
		return Response{ID: r.ID, OK: true, Snapshot: h.snapshot()}
	default:
		return reject("UNSUPPORTED_COMMAND")
	}
}

// NetworkChanged is invoked by the trusted service, never selected by a JSON
// caller. It cancels the current attempt and retains protection until a fresh
// eligible connection is prepared.
func (h *Host) NetworkChanged() error {
	if !h.networkChanged {
		up, down := h.engine.Counters()
		h.baseUpload = h.addCounter(h.baseUpload, up)
		h.baseDownload = h.addCounter(h.baseDownload, down)
	}
	h.networkChanged = true
	return h.pause()
}
func (h *Host) addCounter(base, value uint64) uint64 {
	if ^uint64(0)-base < value {
		h.trafficIncomplete = true
		return ^uint64(0)
	}
	return base + value
}

func Run(input io.Reader, output io.Writer) error {
	h, err := New(localProxyPort)
	if err != nil {
		return err
	}
	return h.run(input, output)
}

func (h *Host) run(input io.Reader, output io.Writer) (err error) {
	defer func() { err = errors.Join(err, h.Close()) }()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 65537)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) > 65536 {
			return errors.New("INVALID_REQUEST")
		}
		request, decodeErr := Decode(line)
		if decodeErr != nil {
			return decodeErr
		}
		response, marshalErr := json.Marshal(h.Handle(request))
		if marshalErr != nil || len(response) > 65536 {
			return errors.New("RESPONSE_TOO_LARGE")
		}
		response = append(response, '\n')
		if _, err = output.Write(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func Decode(data []byte) (Request, error) {
	var r Request
	if control.ValidateJSON(data) != nil {
		return r, errors.New("INVALID_REQUEST")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return Request{}, errors.New("INVALID_REQUEST")
	}
	return r, nil
}

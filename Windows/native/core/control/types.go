package control

import (
	"bluegrass/native/core/clock"
	"bluegrass/native/core/engine"
	"time"
)

type ResourceKey = engine.ResourceKey
type ConnectionProfile = engine.Profile
type Owner struct {
	Environment string
	DataSource  string
	AccountKey  string
	ClientEpoch string
}
type ControlContext struct {
	SchemaVersion    int         `json:"schemaVersion"`
	ClientEpoch      string      `json:"clientEpoch"`
	RuntimeEpoch     string      `json:"runtimeEpoch"`
	LogicalSessionID string      `json:"logicalSessionId"`
	AttemptID        string      `json:"attemptId"`
	ResourceKey      ResourceKey `json:"resourceKey"`
}
type Command string

const (
	Hello               Command = "Hello"
	GetStatus           Command = "GetStatus"
	Connect             Command = "Connect"
	Pause               Command = "Pause"
	Disconnect          Command = "Disconnect"
	ReleaseProtection   Command = "ReleaseProtection"
	ApplyResourceUpdate Command = "ApplyResourceUpdate"
)

type ResourceUpdate struct {
	Revision      uint64     `json:"revision"`
	ValidUntilUTC *time.Time `json:"validUntilUtc"`
	Unavailable   bool       `json:"unavailable"`
	// A changed configuration invalidates the attempt; a fresh Connect must verify it.
	Profile *ConnectionProfile `json:"profile,omitempty"`
}
type Parameters struct {
	Profile *ConnectionProfile `json:"profile,omitempty"`
	Update  *ResourceUpdate    `json:"update,omitempty"`
}
type ControlRequest struct {
	Context   ControlContext `json:"context"`
	RequestID string         `json:"requestId"`
	Sequence  uint64         `json:"sequence"`
	Command   Command        `json:"command"`
	Params    Parameters     `json:"params"`
}
type CommandAck struct {
	RequestID   string         `json:"requestId"`
	Disposition string         `json:"disposition"`
	ErrorCode   string         `json:"errorCode,omitempty"`
	Snapshot    NativeSnapshot `json:"snapshot"`
}
type TrafficSnapshot struct {
	Context        ControlContext `json:"context"`
	CounterEpoch   string         `json:"counterEpoch"`
	Sequence       uint64         `json:"sequence"`
	MonotonicNanos int64          `json:"monotonicNanos"`
	UploadBytes    string         `json:"uploadBytes"`
	DownloadBytes  string         `json:"downloadBytes"`
	Available      bool           `json:"available"`
	Complete       bool           `json:"complete"`
}
type NativeSnapshot struct {
	RuntimeEpoch     string            `json:"runtimeEpoch"`
	Context          *ControlContext   `json:"context"`
	EventSequence    uint64            `json:"eventSequence"`
	Connection       string            `json:"connection"`
	ActualMode       string            `json:"actualMode"`
	ProtectionIntent bool              `json:"protectionIntent"`
	Protection       string            `json:"protection"`
	BusinessGateOpen bool              `json:"businessGateOpen"`
	GateKnown        bool              `json:"gateKnown"`
	VerifiedExitIPv4 string            `json:"verifiedExitIpv4,omitempty"` // Current optional observation, never an admission proof.
	Expired          bool              `json:"expired"`
	ValidUntilUTC    *time.Time        `json:"validUntilUtc"`
	Time             clock.TimeReading `json:"time"`
	Traffic          TrafficSnapshot   `json:"traffic"`
	ErrorCode        string            `json:"errorCode,omitempty"`
}

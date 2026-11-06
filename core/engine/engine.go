// Package engine defines the in-process boundary; it supplies no network implementation.
package engine

import (
	"context"
	"time"

	"bluegrass/native/core/protocolconfig"
)

type ResourceKey struct {
	Environment string `json:"environment"`
	DataSource  string `json:"dataSource"`
	AccountKey  string `json:"accountKey"`
	Kind        string `json:"kind"`
	SourceID    string `json:"sourceId"`
}

type Entry struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}
type Auth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type Profile struct {
	Key            ResourceKey            `json:"key"`
	Protocol       string                 `json:"protocol"`
	Entry          Entry                  `json:"entry"`
	Auth           Auth                   `json:"auth"`
	ProtocolConfig *protocolconfig.Config `json:"protocolConfig,omitempty"`
	// Deprecated: accepted only for legacy IPC compatibility. Never used for
	// eligibility, connection admission, or comparison with an observed exit.
	ExpectedExitIPv4  string     `json:"expectedExitIpv4,omitempty"`
	ValidUntilUTC     *time.Time `json:"validUntilUtc"`
	ConfigVersion     uint64     `json:"configVersion"`
	ConfigFingerprint string     `json:"configFingerprint"`
	Mode              string     `json:"mode"`
	RoutingMode       string     `json:"routingMode,omitempty"`
}

// EffectiveProtocol selects one validated source of protocol parameters. Typed
// resources never inherit legacy Auth. An explicit config rejects duplicate
// legacy credentials except an exact copy of typed SOCKS5 authentication.
// The controller detaches accepted Profile
// values before asynchronous use; callers must not mutate them during Prepare.
func (p Profile) EffectiveProtocol() (protocolconfig.Config, error) {
	var config protocolconfig.Config
	if p.ProtocolConfig != nil {
		config = *p.ProtocolConfig
	} else {
		if p.Protocol != "socks5" {
			return protocolconfig.Config{}, protocolconfig.ErrInvalidConfig
		}
		config = protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: p.Auth.Username, Password: p.Auth.Password}}
	}
	if config.Type != p.Protocol || config.Validate() != nil {
		return protocolconfig.Config{}, protocolconfig.ErrInvalidConfig
	}
	if p.ProtocolConfig != nil && p.Auth != (Auth{}) &&
		(config.Type != "socks5" || p.Auth.Username != config.SOCKS5.Username || p.Auth.Password != config.SOCKS5.Password) {
		return protocolconfig.Config{}, protocolconfig.ErrInvalidConfig
	}
	return config, nil
}

type ProbeResult struct {
	IPv4          string
	Authenticated bool
	Reachable     bool
}

// All calls are serialized, including ProbeExit. Prepare MUST leave business
// forwarding closed. After Prepare succeeds the controller opens the gate and
// commits connected before ProbeExit, which is an advisory current-path probe.
// Its failure must not close forwarding. Stop tears down the session but MUST retain closed-gate
// protection. Implementations must honor ctx cancellation promptly; a stuck
// implementation stalls subsequent operations rather than running them concurrently.
// SetBusinessGate is synchronous. An error does not prove the gate's actual state.
type Engine interface {
	Prepare(context.Context, Profile) error
	ProbeExit(context.Context) (ProbeResult, error)
	SetBusinessGate(bool) error
	Stop(context.Context) error
}

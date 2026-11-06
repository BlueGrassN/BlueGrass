// Package mactun owns the privileged Darwin network transaction. It accepts
// typed engine profiles, never paths, commands or firewall/configuration text
// from IPC. The portable state machine is tested without administrator rights.
package mactun

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"bluegrass/native/core/tunengine"
)

var (
	ErrOwner          = errors.New("OWNER_MISMATCH")
	ErrRecovery       = errors.New("RECOVERY_REQUIRED")
	ErrExternalWriter = errors.New("NETWORK_EXTERNAL_WRITER")
	ErrUnsupportedPF  = errors.New("PF_CONFIGURATION_UNSUPPORTED")
)

const journalVersion = 1
const adapterName = "utun233"
const anchorName = "com.apple/zz.bluegrass"
const maxPermits = 128

type Route struct {
	Prefix    netip.Prefix `json:"prefix"`
	Interface string       `json:"interface"`
	Gateway   netip.Addr   `json:"gateway,omitempty"`
	Metric    uint32       `json:"metric"`
}
type Resolver struct {
	Service   string       `json:"service"`
	Interface string       `json:"interface"`
	Servers   []netip.Addr `json:"servers"` // empty means automatic, not an empty manual list
	Effective []netip.Addr `json:"effective"`
	DHCP      []netip.Addr `json:"dhcp,omitempty"` // independently observed while manual override is installed
}
type DNSChange struct {
	Before Resolver     `json:"before"`
	After  []netip.Addr `json:"after"`
}
type Underlay struct {
	Interface       string
	Index           uint32
	Gateway, Source netip.Addr
}
type Permit struct {
	Endpoint netip.AddrPort `json:"endpoint"`
	Protocol string         `json:"protocol"`
	UID      uint32         `json:"uid"`
}
type Guard struct {
	OwnerUID      uint32                 `json:"ownerUid"`
	Interface     string                 `json:"interface"`
	TunnelIndex   uint64                 `json:"tunnelIndex,omitempty"`
	Forwarding    bool                   `json:"forwarding"`
	LAN           []tunengine.LocalRoute `json:"lan,omitempty"`
	MDNSInterface string                 `json:"mdnsInterface,omitempty"`
	Permits       []Permit               `json:"permits,omitempty"`
}

// Journal contains recovery metadata only. Resource identity is a SHA-256 hash;
// profiles, credentials, bearer tokens and DNS question names never enter it.
type Journal struct {
	Version      int         `json:"version"`
	OwnerUID     uint32      `json:"ownerUid"`
	ID           string      `json:"id"`
	ResourceHash string      `json:"resourceHash,omitempty"`
	Session      string      `json:"session,omitempty"`
	Active       bool        `json:"active"`
	Token        string      `json:"token,omitempty"`
	Enabling     bool        `json:"enabling,omitempty"`
	Releasing    bool        `json:"releasing,omitempty"`
	Rules        string      `json:"rules,omitempty"`
	PendingRules string      `json:"pendingRules,omitempty"`
	Routes       []Route     `json:"routes,omitempty"`
	DNS          []DNSChange `json:"dns,omitempty"`
}

// Backend is the OS boundary. All methods must honor context and compare exact
// ownership before mutation. Only the platform constructor supplies a Backend;
// this is not an IPC interface. A fake can exercise every crash boundary.
type Backend interface {
	LoadJournal() (*Journal, error)
	SaveJournal(Journal) error
	RemoveJournal() error
	Marker(owner uint32, active bool) error
	Close() error
	CheckPF(context.Context, bool) error
	ReadPF(context.Context) (string, error)
	CompilePF(context.Context, string) (string, error)
	WritePF(context.Context, string) error
	EnablePF(context.Context) (string, error)
	PFEnabled(context.Context) error
	ReleasePF(context.Context, string) error
	TokenExists(context.Context, string) (bool, error)
	Network(context.Context, map[string][]netip.Addr, []Route) (tunengine.NetworkSnapshot, error)
	Underlay(context.Context) (Underlay, error)
	Resolvers(context.Context) ([]Resolver, error)
	ReadDNS(context.Context, string) ([]netip.Addr, error)
	VerifyResolver(context.Context, Resolver) error
	WriteDNS(context.Context, string, []netip.Addr) error
	ReadRoute(context.Context, netip.Prefix) (*Route, error)
	AddRoute(context.Context, Route) error
	DeleteRoute(context.Context, Route) error
	VerifyAdapter(context.Context, uint64) error
	Dial(context.Context, string, netip.AddrPort, Underlay) (net.Conn, error)
}

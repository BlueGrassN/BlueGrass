package winprotect

import (
	"net/netip"

	"bluegrass/native/core/gate"
)

type link struct {
	LUID     uint64
	Prefixes []netip.Prefix
}
type policy struct {
	Service, UI  string
	UpdateHelper string
	Entry        netip.AddrPort
	EntryPolicy  EntryPolicy
	DNS          []DNSEndpoint
	ControlDNS   []DNSEndpoint
	AllowLAN     bool
	Links        []link
	Virtual      []netip.Prefix
	Tunnel       uint64
	LocalOnly    bool
	LocalTunnel  uint64
	UDPRelays    []netip.AddrPort
	Direct       []directEndpoint
}
type rule struct {
	Kind                                    string
	Boot, Persistent, IPv6, Inbound, Permit bool
	Weight                                  uint8
	App                                     string
	Remote                                  netip.Prefix
	Protocol                                uint8
	RemotePort, LocalPort                   uint16
	Loopback, NoDNS                         bool
	LUID                                    uint64
}

func defaultVirtualPrefixes() []netip.Prefix {
	return prefixes("198.18.0.0/15", "fc00::/18", "172.19.0.0/30",
		"172.31.255.0/30", "172.30.255.0/30", "10.255.255.0/30",
		"fdfe:dcba:9876::/126", "fdde:adbe:ef01::/126")
}
func prefixes(values ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(values))
	for _, s := range values {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}
func isVirtual(a netip.Addr, pools []netip.Prefix) bool {
	for _, p := range pools {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func compilePolicy(p policy) []rule {
	var out []rule
	for _, v6 := range []bool{false, true} {
		for _, inbound := range []bool{false, true} {
			base := rule{IPv6: v6, Inbound: inbound, Persistent: true}
			block := base
			block.Kind = "block"
			block.Weight = 1
			out = append(out, block)
			boot := block
			boot.Boot = true
			boot.Persistent = false
			out = append(out, boot)
			loop := base
			loop.Kind = "loopback"
			loop.Permit = true
			loop.Weight = 250
			loop.Loopback = true
			out = append(out, loop)
			if p.Tunnel != 0 {
				tun := base
				tun.Kind = "tunnel"
				tun.Permit = true
				tun.Weight = 240
				tun.LUID = p.Tunnel
				tun.Persistent = false
				out = append(out, tun)
			}
			if p.LocalTunnel != 0 {
				for _, protocol := range []uint8{6, 17} {
					r := base
					r.Kind, r.Protocol, r.RemotePort = "local-tunnel-dns", protocol, 53
					r.LUID, r.Weight, r.Permit, r.Persistent = p.LocalTunnel, 240, true, false
					out = append(out, r)
				}
			}
			// Explicit virtual blocks outrank every physical-network exception.
			for _, pool := range p.Virtual {
				if pool.Addr().Is6() == v6 {
					r := base
					r.Kind = "virtual"
					r.Remote = pool
					r.Weight = 230
					out = append(out, r)
				}
			}
			if !inbound {
				ui := base
				ui.Kind = "control"
				ui.App = p.UI
				ui.Protocol = 6
				ui.RemotePort = 443
				ui.Permit = true
				ui.Weight = 220
				for _, app := range []string{p.UI, p.UpdateHelper} {
					if app != "" {
						ui.App = app
						out = append(out, ui)
					}
				}
				endpoints := append([]DNSEndpoint(nil), p.DNS...)
				endpoints = append(endpoints, p.ControlDNS...)
				if p.Entry.IsValid() {
					if p.EntryPolicy.TCP {
						endpoints = append(endpoints, DNSEndpoint{p.Entry, "tcp"})
					}
					if p.EntryPolicy.UDP {
						endpoints = append(endpoints, DNSEndpoint{p.Entry, "udp"})
					}
				}
				for _, endpoint := range endpoints {
					ep := endpoint.Endpoint
					if ep.Addr().Is6() != v6 {
						continue
					}
					var proto uint8
					switch endpoint.Transport {
					case "tcp":
						proto = 6
					case "udp":
						proto = 17
					default:
						continue // Never turn a malformed transport into an all-protocol rule.
					}
					r := base
					r.Kind = "service-endpoint"
					if p.LocalOnly {
						r.Persistent = false
					}
					r.App = p.Service
					r.Protocol = proto
					r.Remote = netip.PrefixFrom(ep.Addr(), ep.Addr().BitLen())
					r.RemotePort = ep.Port()
					r.Permit = true
					r.Weight = 220
					if p.Service != "" {
						out = append(out, r)
					}
				}
				if !v6 && p.Tunnel != 0 && !p.LocalOnly && p.Service != "" {
					for _, grant := range p.Direct {
						if !validDirectEndpoint(grant, p.Virtual) {
							continue
						}
						r := base
						r.Kind = "direct"
						r.App = p.Service
						r.Protocol = 6
						if grant.Network == "udp" {
							r.Protocol = 17
						}
						r.Remote = netip.PrefixFrom(grant.Endpoint.Addr(), 32)
						r.RemotePort = grant.Endpoint.Port()
						r.Permit = true
						r.Weight = 220
						r.Persistent = false
						out = append(out, r)
					}
				}
				if !v6 && p.Tunnel != 0 && p.Service != "" && p.EntryPolicy.AuthenticatedSOCKSRelay {
					for _, ep := range p.UDPRelays {
						if !ep.IsValid() || !gate.PublicIPv4(ep.Addr().String()) || ep.Port() == 0 || isVirtual(ep.Addr(), p.Virtual) {
							continue
						}
						r := base
						r.Kind = "udp-relay"
						r.App = p.Service
						r.Protocol = 17
						r.Remote = netip.PrefixFrom(ep.Addr(), 32)
						r.RemotePort = ep.Port()
						r.Permit = true
						r.Weight = 220
						r.Persistent = false
						out = append(out, r)
					}
				}
			}
			if v6 {
				// ALE uses IP_LOCAL_PORT as ICMP type and IP_REMOTE_PORT as code.
				for _, typ := range []uint16{133, 134, 135, 136} {
					for _, target := range prefixes("fe80::/10", "ff02::/16", "::/128") {
						r := base
						r.Kind = "neighbor-discovery"
						r.Protocol = 58
						r.LocalPort = typ
						r.Remote = target
						r.Permit = true
						r.Weight = 210
						out = append(out, r)
					}
				}
				r := base
				r.Kind = "dhcpv6"
				r.Protocol = 17
				r.LocalPort = 546
				r.RemotePort = 547
				r.Permit = true
				r.Weight = 210
				if inbound {
					r.Remote = netip.MustParsePrefix("fe80::/10")
				} else {
					r.Remote = netip.MustParsePrefix("ff02::1:2/128")
				}
				out = append(out, r)
			} else {
				r := base
				r.Kind = "dhcpv4"
				r.Protocol = 17
				r.LocalPort = 68
				r.RemotePort = 67
				r.Permit = true
				r.Weight = 210
				out = append(out, r)
			}
			if p.AllowLAN {
				links := append([]link(nil), p.Links...)
				if p.LocalTunnel != 0 {
					local := link{LUID: p.LocalTunnel}
					for _, l := range p.Links {
						local.Prefixes = append(local.Prefixes, l.Prefixes...)
					}
					links = append(links, local)
				}
				for _, l := range links {
					if l.LUID == 0 || l.LUID == p.Tunnel {
						continue
					}
					ps := prefixes("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "fc00::/7", "fe80::/10", "224.0.0.0/24", "239.0.0.0/8", "255.255.255.255/32", "ff01::/16", "ff02::/16")
					ps = append(ps, l.Prefixes...)
					for _, prefix := range ps {
						if !prefix.IsValid() || prefix.Bits() == 0 || prefix.Addr().Is6() != v6 {
							continue
						}
						for _, proto := range []uint8{6, 17} {
							r := base
							r.Kind = "lan"
							if l.LUID == p.LocalTunnel {
								r.Persistent = false
							}
							r.Protocol = proto
							r.NoDNS = true
							r.Remote = prefix.Masked()
							r.LUID = l.LUID
							r.Weight = 100
							r.Permit = true
							out = append(out, r)
						}
						r := base
						r.Kind = "lan-icmp"
						if l.LUID == p.LocalTunnel {
							r.Persistent = false
						}
						r.Protocol = 1
						if v6 {
							r.Protocol = 58
						}
						r.Remote = prefix.Masked()
						r.LUID = l.LUID
						r.Weight = 100
						r.Permit = true
						out = append(out, r)
					}
				}
			}
		}
	}
	return out
}

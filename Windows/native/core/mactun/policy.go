package mactun

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"bluegrass/native/core/gate"
)

var interfacePattern = regexp.MustCompile(`^en[0-9]{1,3}$`)
var controlPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
var opaqueID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validControlHost(host string) bool {
	if len(host) > 253 || !controlPattern.MatchString(host) {
		return false
	}
	for _, suffix := range []string{"local", "localhost", "internal", "invalid", "test", "onion", "home.arpa", "arpa"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return false
		}
	}
	return true
}
func validEndpoint(e netip.AddrPort) bool {
	return e.IsValid() && e.Port() != 0 && e.Addr().Is4() && e.Addr().IsGlobalUnicast() && e.Addr().Zone() == "" && !e.Addr().IsLoopback() && !netip.MustParsePrefix("198.18.0.0/15").Contains(e.Addr())
}
func publicEndpoint(e netip.AddrPort) bool {
	return validEndpoint(e) && gate.PublicIPv4(e.Addr().String())
}
func validRoute(r Route) bool {
	if !r.Prefix.IsValid() || !r.Prefix.Addr().Is4() || r.Prefix != r.Prefix.Masked() || r.Metric != 0 {
		return false
	}
	if r.Interface == adapterName {
		return !r.Gateway.IsValid() && (r.Prefix.String() == "0.0.0.0/1" || r.Prefix.String() == "128.0.0.0/1")
	}
	return interfacePattern.MatchString(r.Interface) && r.Prefix.Bits() == 32 && r.Gateway.Is4() && r.Gateway.IsGlobalUnicast() && !r.Gateway.IsLoopback()
}
func validService(s string) bool {
	return len(s) > 0 && len(s) <= 256 && !strings.ContainsAny(s, "\x00\r\n") && !strings.HasPrefix(s, "-")
}
func validAddresses(a []netip.Addr) bool {
	if len(a) > 16 {
		return false
	}
	for _, ip := range a {
		if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() || ip.IsMulticast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

// Stateless rules are intentional. Kernel state created by a pass rule could
// survive removal of its permit or the worker. Reply rules are equally precise.
// We refuse pre-existing foreign states rather than flushing global PF state.
func renderGuard(g Guard) (string, error) {
	if g.OwnerUID == 0 || g.Interface != "" && !interfacePattern.MatchString(g.Interface) || g.Interface == "" && (g.Forwarding || len(g.Permits) > 0 || len(g.LAN) > 0) || len(g.Permits) > maxPermits || len(g.LAN) > 128 || g.Forwarding && g.TunnelIndex == 0 {
		return "", errors.New("INVALID_PF_POLICY")
	}
	lines := []string{"pass quick on lo0 all no state label \"bluegrass-loopback\""}
	if g.Forwarding {
		lines = append(lines, "pass quick on "+adapterName+" inet all no state label \"bluegrass-tunnel\"")
	}
	// DHCP is link scoped; it does not grant an arbitrary root/public UDP bypass.
	if g.Interface != "" {
		lines = append(lines,
			fmt.Sprintf("pass out quick on %s inet proto udp from any port 68 to 255.255.255.255 port 67 no state label \"bluegrass-dhcp\"", g.Interface),
			fmt.Sprintf("pass in quick on %s inet proto udp from any port 67 to any port 68 no state label \"bluegrass-dhcp\"", g.Interface))
	}
	for _, r := range g.LAN {
		if !interfacePattern.MatchString(r.Interface) || !r.Prefix.IsValid() || r.Prefix != r.Prefix.Masked() || r.Prefix.Bits() == 0 || r.Prefix.Addr().Is4In6() {
			return "", errors.New("INVALID_LAN_SCOPE")
		}
		ip := r.Prefix.Addr()
		local := ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || r.Scoped && (ip.IsMulticast() || ip.String() == "255.255.255.255")
		if !local && !r.Connected {
			return "", errors.New("INVALID_LAN_SCOPE")
		}
		// Never allow a public IPv6 connected prefix to bypass the IPv6 policy.
		if ip.Is6() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() {
			continue
		}
		family := "inet"
		if ip.Is6() {
			family = "inet6"
		}
		lines = append(lines, fmt.Sprintf("pass out quick on %s %s to %s no state label \"bluegrass-lan\"", r.Interface, family, r.Prefix), fmt.Sprintf("pass in quick on %s %s from %s no state label \"bluegrass-lan\"", r.Interface, family, r.Prefix))
	}
	if g.MDNSInterface != "" {
		if g.MDNSInterface != g.Interface || !interfacePattern.MatchString(g.MDNSInterface) {
			return "", errors.New("INVALID_MDNS_SCOPE")
		}
		// Link-local discovery is a LAN exception, not a public UDP exception.
		// macOS's mDNSResponder is not root, so this exact multicast scope is
		// available to local clients when LAN is enabled.
		lines = append(lines,
			fmt.Sprintf("pass out quick on %s inet proto udp to 224.0.0.251 port 5353 no state label \"bluegrass-mdns\"", g.MDNSInterface),
			fmt.Sprintf("pass out quick on %s inet6 proto udp to ff02::fb port 5353 no state label \"bluegrass-mdns\"", g.MDNSInterface),
			fmt.Sprintf("pass in quick on %s inet6 proto udp from fe80::/10 port 5353 to ff02::fb port 5353 no state label \"bluegrass-mdns\"", g.MDNSInterface),
			fmt.Sprintf("pass quick on %s inet6 proto icmp6 to { fe80::/10, ff02::/16 } no state label \"bluegrass-lan-nd\"", g.MDNSInterface))
	}
	permits := append([]Permit(nil), g.Permits...)
	sort.Slice(permits, func(i, j int) bool { return fmt.Sprint(permits[i]) < fmt.Sprint(permits[j]) })
	for _, p := range permits {
		if !validEndpoint(p.Endpoint) || p.Protocol != "tcp" && p.Protocol != "udp" || p.UID != 0 && p.UID != g.OwnerUID {
			return "", errors.New("INVALID_PF_PERMIT")
		}
		if p.UID == g.OwnerUID && (p.Protocol != "tcp" || p.Endpoint.Port() != 443) {
			return "", errors.New("INVALID_CONTROL_PERMIT")
		}
		flags := ""
		if p.Protocol == "tcp" {
			flags = " flags any"
		}
		label := "bluegrass-peer"
		if p.UID == g.OwnerUID {
			label = "bluegrass-control"
		}
		lines = append(lines, fmt.Sprintf("pass out quick on %s inet proto %s to %s port %d user %d%s no state label \"%s\"", g.Interface, p.Protocol, p.Endpoint.Addr(), p.Endpoint.Port(), p.UID, flags, label), fmt.Sprintf("pass in quick on %s inet proto %s from %s port %d%s no state label \"%s\"", g.Interface, p.Protocol, p.Endpoint.Addr(), p.Endpoint.Port(), flags, label))
	}
	lines = append(lines, "block drop out quick all label \"bluegrass-block-out\"", "block drop in quick inet6 all label \"bluegrass-block-v6\"")
	return strings.Join(lines, "\n") + "\n", nil
}
func validateGuardRules(rules string) error {
	if !strings.Contains(rules, `label "bluegrass-block-out"`) || !strings.Contains(rules, `label "bluegrass-block-v6"`) || strings.Contains(rules, "keep state") || strings.Contains(rules, "modulate state") || strings.Contains(rules, "synproxy state") {
		return ErrRecovery
	}
	for _, line := range strings.Split(rules, "\n") {
		if strings.HasPrefix(line, "pass ") && !strings.Contains(line, "no state") {
			return ErrRecovery
		}
	}
	return nil
}

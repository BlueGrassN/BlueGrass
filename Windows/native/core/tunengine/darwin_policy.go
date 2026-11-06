package tunengine

// Pure Darwin policy helpers also run on the development host. OS calls stay
// in *_darwin.go; these helpers never change Windows platform behavior.
import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"

	tun "github.com/xtls/xray-core/proxy/tun"
	"google.golang.org/protobuf/proto"
)

// OwnedPhysicalRoute is a precise route identity already verified by the
// trusted root manager. It is used by Darwin's SystemNetworkWithOwned API;
// never populate it from GUI/IPC input.
type OwnedPhysicalRoute struct {
	Prefix    netip.Prefix
	Interface string
	Gateway   netip.Addr
	Metric    uint32
}

func darwinWithoutOwnedRoutes(adapters []systemAdapter, routes []systemRoute, ignored []OwnedPhysicalRoute) []systemRoute {
	if len(ignored) == 0 {
		return routes
	}
	names := make(map[uint64]string, len(adapters))
	for _, a := range adapters {
		names[a.LUID] = a.Name
	}
	owned := make(map[OwnedPhysicalRoute]struct{}, len(ignored))
	for _, r := range ignored {
		if r.Prefix.IsValid() && r.Gateway.IsValid() && darwinPhysicalName(r.Interface) {
			owned[r] = struct{}{}
		}
	}
	filtered := make([]systemRoute, 0, len(routes))
	for _, r := range routes {
		key := OwnedPhysicalRoute{Prefix: r.Prefix, Interface: names[r.LUID], Gateway: r.NextHop, Metric: r.Metric}
		if _, ok := owned[key]; !ok {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// Layout from Apple's bsd/sys/kauth.h and bsd/vfs/vfs_attrlist.c. With only
// ATTR_CMN_EXTENDED_SECURITY requested: uint32 length, attrreference_t, then
// optional kauth_filesec. Accept absence or the exact NOACL sentinel; an
// explicitly empty ACL is still an ACL and is refused.
func darwinNoACLBuffer(buffer []byte) bool {
	if len(buffer) < 12 {
		return false
	}
	total := binary.LittleEndian.Uint32(buffer[:4])
	offset := binary.LittleEndian.Uint32(buffer[4:8])
	size := binary.LittleEndian.Uint32(buffer[8:12])
	if total > uint32(len(buffer)) || offset != 8 {
		return false
	}
	if size == 0 {
		return total == 12
	}
	if size != 44 || total != 56 || binary.LittleEndian.Uint32(buffer[12:16]) != 0x012cc16d {
		return false
	}
	for _, b := range buffer[16:48] {
		if b != 0 {
			return false
		}
	}
	return binary.LittleEndian.Uint32(buffer[48:52]) == 0xffffffff && binary.LittleEndian.Uint32(buffer[52:56]) == 0
}

func darwinOwnedOptions(options *tun.Config) *tun.Config {
	out := proto.Clone(options).(*tun.Config)
	out.AutoSystemRoutingTable = nil
	out.DNS = nil
	out.AutoSystemDnsToGateway = false
	out.AutoOutboundsInterface = ""
	out.AutoSystemWfpBlockLeak = nil
	return out
}

func darwinOriginalDNS(adapters []systemAdapter, originals map[string][]netip.Addr) []systemAdapter {
	out := append([]systemAdapter(nil), adapters...)
	for i := range out {
		if dns, ok := originals[out[i].Name]; ok {
			out[i].DNS = append([]netip.Addr(nil), dns...)
		}
	}
	return out
}

func darwinPhysicalName(name string) bool {
	// Darwin Ethernet/Wi-Fi, bridges, bonded links and VLANs. An allowlist
	// avoids silently turning a new VPN interface type into an underlay.
	for _, prefix := range []string{"en", "bridge", "bond", "vlan"} {
		suffix, ok := strings.CutPrefix(name, prefix)
		if !ok || suffix == "" {
			continue
		}
		valid := true
		for _, c := range suffix {
			if c < '0' || c > '9' {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return false
}

// scutil exposes resolver service/interface associations without resolving any
// name. Supplemental domain resolvers must not become default bootstrap DNS.
func parseDarwinDNS(raw, defaultInterface string) (map[string][]netip.Addr, error) {
	result := map[string][]netip.Addr{}
	var servers []netip.Addr
	iface := ""
	supplemental := false
	flush := func() {
		target := iface
		if target == "" && !supplemental {
			target = defaultInterface
		}
		if target != "" && !supplemental {
			for _, ip := range servers {
				found := false
				for _, old := range result[target] {
					found = found || old == ip
				}
				if !found {
					result[target] = append(result[target], ip)
				}
			}
		}
		servers = nil
		iface = ""
		supplemental = false
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "resolver #") || strings.HasPrefix(line, "DNS configuration") {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case strings.HasPrefix(key, "nameserver["):
			ip, err := netip.ParseAddr(value)
			if err != nil {
				return nil, errors.New("DARWIN_DNS_READ_FAILED")
			}
			servers = append(servers, ip.Unmap())
		case key == "if_index":
			left, right := strings.IndexByte(value, '('), strings.IndexByte(value, ')')
			if left < 0 || right <= left+1 {
				return nil, errors.New("DARWIN_DNS_SCOPE_INVALID")
			}
			iface = value[left+1 : right]
		case key == "domain":
			supplemental = true
		}
	}
	flush()
	return result, nil
}

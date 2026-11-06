package tunengine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/netip"
	"sort"
	"strings"
)

type systemAdapter struct {
	Name             string
	LUID             uint64
	Index, Index6    uint32
	Up               bool
	Metric4, Metric6 uint32
	Addresses        []netip.Prefix
	DNS              []netip.Addr
}
type systemRoute struct {
	LUID    uint64
	Prefix  netip.Prefix
	NextHop netip.Addr
	Metric  uint32
}

func systemSnapshot(adapters []systemAdapter, routes []systemRoute) NetworkSnapshot {
	byLUID := map[uint64]systemAdapter{}
	var canonicalAdapters []systemAdapter
	for _, a := range adapters {
		if strings.EqualFold(a.Name, InterfaceName) {
			continue
		}
		a.Addresses = append([]netip.Prefix(nil), a.Addresses...)
		a.DNS = append([]netip.Addr(nil), a.DNS...)
		sort.Slice(a.Addresses, func(i, j int) bool { return a.Addresses[i].String() < a.Addresses[j].String() })
		byLUID[a.LUID] = a
		canonicalAdapters = append(canonicalAdapters, a)
	}
	sort.Slice(canonicalAdapters, func(i, j int) bool { return canonicalAdapters[i].LUID < canonicalAdapters[j].LUID })
	var canonicalRoutes []systemRoute
	for _, r := range routes {
		if _, ok := byLUID[r.LUID]; ok {
			canonicalRoutes = append(canonicalRoutes, r)
		}
	}
	sort.Slice(canonicalRoutes, func(i, j int) bool {
		a, b := canonicalRoutes[i], canonicalRoutes[j]
		if a.LUID != b.LUID {
			return a.LUID < b.LUID
		}
		if a.Prefix != b.Prefix {
			return a.Prefix.String() < b.Prefix.String()
		}
		if a.NextHop != b.NextHop {
			return a.NextHop.String() < b.NextHop.String()
		}
		return a.Metric < b.Metric
	})
	raw, _ := json.Marshal(struct {
		Adapters []systemAdapter
		Routes   []systemRoute
	}{canonicalAdapters, canonicalRoutes})
	hash := sha256.Sum256(raw)
	n := NetworkSnapshot{Generation: binary.LittleEndian.Uint64(hash[:8])}
	if n.Generation == 0 {
		n.Generation = 1
	}
	occupied := map[netip.Prefix]bool{}
	for _, a := range canonicalAdapters {
		if !a.Up {
			continue
		}
		for _, p := range a.Addresses {
			if p.IsValid() && p.Bits() > 0 {
				occupied[p.Masked()] = true
			}
		}
	}
	var defaultLUID uint64
	bestMetric := uint64(math.MaxUint64)
	ambiguousDefault := false
	for _, r := range canonicalRoutes {
		a := byLUID[r.LUID]
		if !a.Up || !r.Prefix.IsValid() || !snapshotPhysicalInterface(a.Name) {
			continue
		}
		metric := uint64(r.Metric)
		if r.Prefix.Addr().Is4() {
			metric += uint64(a.Metric4)
		} else {
			metric += uint64(a.Metric6)
		}
		if r.Prefix.Bits() == 0 {
			if r.Prefix.Addr().Is4() {
				if metric < bestMetric {
					defaultLUID, bestMetric, ambiguousDefault = r.LUID, metric, false
				} else if metric == bestMetric && r.LUID != defaultLUID {
					ambiguousDefault = true
				}
			}
			continue
		}
		occupied[r.Prefix.Masked()] = true
		connected := false
		if r.NextHop.IsValid() && r.NextHop.IsUnspecified() {
			for _, p := range a.Addresses {
				if p.Masked().Contains(r.Prefix.Addr()) && r.Prefix.Bits() >= p.Bits() {
					connected = true
					break
				}
			}
		}
		scoped := within(r.Prefix, localScoped)
		if within(r.Prefix, localUnicast) || connected && r.Prefix.Addr().IsGlobalUnicast() || scoped {
			if metric > math.MaxUint32 {
				metric = math.MaxUint32
			}
			n.Routes = append(n.Routes, LocalRoute{Prefix: r.Prefix.Masked(), Interface: a.Name, Metric: uint32(metric), Connected: connected, Scoped: scoped})
		}
	}
	for p := range occupied {
		n.Occupied = append(n.Occupied, p)
	}
	// The TUN packet does not carry the application's original IPv6 scope. If
	// several links cover the same scoped destination, fail closed for that
	// destination instead of choosing a link merely by its route metric.
	var unambiguous []LocalRoute
	for i, a := range n.Routes {
		ambiguous := false
		for j, b := range n.Routes {
			if i != j && ambiguousScope(a, b) {
				ambiguous = true
				break
			}
		}
		if !ambiguous {
			unambiguous = append(unambiguous, a)
		}
	}
	n.Routes = unambiguous
	sort.Slice(n.Occupied, func(i, j int) bool { return n.Occupied[i].String() < n.Occupied[j].String() })
	// Generic search suffixes are NOT administrator DNS-routing-domain evidence.
	// Automatic lan/home.arpa/mDNS belong only to the unambiguous default link.
	// Other explicit domain mappings can be supplied by a trusted Network provider.
	if defaultLUID != 0 && !ambiguousDefault {
		a := byLUID[defaultLUID]
		n.MDNSInterface = a.Name
		for _, server := range a.DNS {
			if server.IsValid() && !server.IsUnspecified() && !server.IsLoopback() && !server.IsMulticast() {
				n.EntryDNS = append(n.EntryDNS, netip.AddrPortFrom(server, 53))
			}
		}
		for _, server := range a.DNS {
			if !within(netip.PrefixFrom(server.WithZone(""), server.BitLen()), localUnicast) {
				continue
			}
			reachable := false
			for _, r := range n.Routes {
				if r.Interface == a.Name && r.Prefix.Contains(server.WithZone("")) {
					reachable = true
					break
				}
			}
			if reachable {
				n.DNS = append(n.DNS, LocalDNS{Server: netip.AddrPortFrom(server, 53), Interface: a.Name, Domains: []string{"lan", "home.arpa"}})
				break // no cross-network fallback and no duplicate domain mappings
			}
		}
	}
	return n
}

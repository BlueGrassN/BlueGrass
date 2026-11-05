//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/serviceipc"
	"bluegrass/native/core/update"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/netip"
)

func resolveViaService(ctx context.Context, i *installation, host string) ([]netip.Addr, error) {
	allowed := false
	for _, h := range TrustedHosts() {
		if h == host {
			allowed = true
		}
	}
	if !allowed {
		return nil, errControlDNS
	}
	client, err := serviceipc.Dial(ctx, i.root, i.publisher, update.TrustedPublisher)
	if err != nil {
		return nil, errControlDNS
	}
	defer client.Close()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errControlDNS
	}
	id := hex.EncodeToString(random[:])
	raw, _ := json.Marshal(map[string]string{"id": id, "command": "ResolveUpdateHost", "host": host})
	response, err := client.Exchange(raw)
	if err != nil || ctx.Err() != nil {
		return nil, errControlDNS
	}
	var r struct {
		ID        string   `json:"id"`
		OK        bool     `json:"ok"`
		Addresses []string `json:"addresses"`
		Error     string   `json:"error"`
	}
	if strictObject(response, &r, "id", "ok", "addresses", "error") != nil || r.ID != id || !r.OK || r.Error != "" || len(r.Addresses) == 0 || len(r.Addresses) > 8 {
		return nil, errControlDNS
	}
	addresses := make([]netip.Addr, 0, len(r.Addresses))
	seen := map[netip.Addr]bool{}
	for _, raw := range r.Addresses {
		ip, err := netip.ParseAddr(raw)
		if err != nil || !publicIPv4(ip) || seen[ip] {
			return nil, errControlDNS
		}
		seen[ip] = true
		addresses = append(addresses, ip)
	}
	return addresses, nil
}

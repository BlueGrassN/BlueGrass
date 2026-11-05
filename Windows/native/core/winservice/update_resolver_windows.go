//go:build windows

package winservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"time"

	"bluegrass/native/core/gate"
	"bluegrass/native/core/serviceipc"
)

type updateResolveRequest struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Host    string `json:"host"`
}
type updateResolveResponse struct {
	ID        string   `json:"id"`
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

// serveUpdateResolver is a one-shot role, separate from Hello/Native Host and
// the session lease. Authentication happens before entry. Only compiled update
// hostnames reach lookup; a helper cannot send Connect, Release or network config.
func serveUpdateResolver(conn net.Conn, hosts []string, lookup func(context.Context, string) ([]netip.Addr, error)) error {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	raw, err := serviceipc.ReadFrame(conn)
	if err != nil {
		return err
	}
	var request updateResolveRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&request)
	var extra any
	trailing := decoder.Decode(&extra)
	response := updateResolveResponse{ID: request.ID}
	if decodeErr != nil || trailing != io.EOF || request.ID == "" || len(request.ID) > 128 || request.Command != "ResolveUpdateHost" || request.Host == "" || !slices.Contains(hosts, request.Host) {
		response.Error = "UPDATE_RESOLVER_REJECTED"
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addresses, err := lookup(ctx, request.Host)
		cancel()
		if err != nil || len(addresses) == 0 || len(addresses) > 16 {
			response.Error = "UPDATE_DNS_FAILED"
		} else {
			for _, ip := range addresses {
				if !gate.PublicIPv4(ip.String()) {
					response.Error = "UPDATE_DNS_FAILED"
					break
				}
				response.Addresses = append(response.Addresses, ip.String())
			}
		}
	}
	response.OK = response.Error == ""
	if !response.OK {
		response.Addresses = nil
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return serviceipc.WriteFrame(conn, data)
}

func (e *protectedTunnel) resolveUpdateHost(ctx context.Context, host string, owner string) ([]netip.Addr, error) {
	// The service worker is authenticated separately and has no session engine.
	// A failed/residual unknown owner can only be recovered by explicit repair.
	if e.protection.Active() && owner != e.sid {
		return nil, errors.New("OWNER_MISMATCH")
	}
	return e.resolveTrustedHost(ctx, host)
}

package main

import (
	"context"
	"net"
	"net/netip"
	"time"
)

func exchangeSTUN(ctx context.Context, conn net.Conn, transaction [12]byte) (netip.AddrPort, int, string) {
	var empty netip.AddrPort
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	// Cancellation also interrupts an in-flight UDP read.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(deadline); err != nil {
		return empty, 0, "stun_io"
	}
	request := bindingRequest(transaction)
	buf := make([]byte, 65535)
	lastCode := "timeout"
	packets := 0
	for attempt := 1; attempt <= 4; attempt++ {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return empty, attempt - 1, lastCode
		}
		if n, err := conn.Write(request); err != nil || n != len(request) {
			if ctx.Err() != nil {
				return empty, attempt - 1, "timeout"
			}
			return empty, attempt - 1, "stun_io"
		}
		// Send at roughly 0, 0.5, 1.5, 3.5 seconds; the final read uses the
		// original deadline. Invalid packets never reset the retry timer.
		until := time.Now().Add(500 * time.Millisecond << (attempt - 1))
		if attempt == 4 || until.After(deadline) {
			until = deadline
		}
		if err := conn.SetReadDeadline(until); err != nil {
			return empty, attempt, "stun_io"
		}
		for {
			n, err := conn.Read(buf)
			if err != nil {
				if ctx.Err() != nil {
					return empty, attempt, lastCode
				}
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					break
				}
				return empty, attempt, "stun_io"
			}
			packets++
			mapping, code := parseBindingResponse(buf[:n], transaction)
			if code == "" {
				return mapping, attempt, ""
			}
			if code == "stun_response_error" {
				return empty, attempt, code
			}
			lastCode = code
			if packets >= 64 {
				return empty, attempt, "stun_packet_limit"
			}
			if !time.Now().Before(until) {
				break
			}
		}
	}
	return empty, 4, lastCode
}

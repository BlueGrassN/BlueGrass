package mactunipc

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// AuthorizationProcessTimeout includes installation and the ready handshake.
// The system dialog itself has a shorter 70-second limit. Leave time inside
// the GUI's 120-second startup budget for the initial host reply and cleanup.
const AuthorizationProcessTimeout = 75 * time.Second

func authorizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, AuthorizationProcessTimeout)
}

type Client struct {
	mu      sync.Mutex
	conn    net.Conn
	timeout time.Duration
}

func (c *Client) Exchange(data []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := compactObject(data); err != nil {
		return nil, err
	}
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		_ = c.conn.Close()
		return nil, ErrUnavailable
	}
	if err := WriteFrame(c.conn, data); err != nil {
		_ = c.conn.Close()
		return nil, ErrUnavailable
	}
	reply, err := ReadFrame(c.conn)
	if err == nil {
		reply, err = compactObject(reply)
	}
	if err != nil {
		_ = c.conn.Close()
		return nil, ErrUnavailable
	}
	return reply, nil
}

// Close deliberately does not wait for Exchange's lock: cancellation interrupts
// a blocked exchange immediately and lets the root host close forwarding.
func (c *Client) Close() error { return c.conn.Close() }

func serveOwned(ctx context.Context, conn net.Conn, owner Identity, alive func(Identity) bool, run func(io.Reader, io.Writer, uint32) error) error {
	if !owner.valid() || !alive(owner) {
		_ = conn.Close()
		return ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !alive(owner) {
					cancel()
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return ServeStream(ctx, conn, owner.UID, run)
}

func authorizationResult(code string) error {
	switch code {
	case "OK":
		return nil
	case "AUTHORIZATION_CANCELLED":
		return ErrAuthorization
	case "AUTHORIZATION_TIMEOUT":
		return ErrAuthorizationTimeout
	case "PRIVILEGED_HELPER_INVALID":
		return ErrHelperInvalid
	case "SERVICE_BUSY":
		return ErrBusy
	default:
		return ErrUnavailable
	}
}

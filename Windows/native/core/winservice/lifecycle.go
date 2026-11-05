package winservice

import (
	"errors"
	"sync"

	"bluegrass/native/core/serviceipc"
)

var (
	errServiceBusy    = errors.New("SERVICE_BUSY")
	errCleanupPending = errors.New("RECOVERY_REQUIRED")
)

// clientLifecycle belongs to the SERVICE, not a pipe worker. A failed Close
// keeps its exact host alive after the pipe lease is released. No next client
// may create an engine (or release protection) until that owned Close succeeds.
type clientLifecycle struct {
	mu      sync.Mutex
	lease   serviceipc.Lease
	pending func() error
}

func (c *clientLifecycle) acquire(identity serviceipc.Identity) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lease.Acquire(identity) {
		return errServiceBusy
	}
	if err := c.retryLocked(); err != nil {
		c.lease.Release(identity)
		return err
	}
	return nil
}

func (c *clientLifecycle) retire(identity serviceipc.Identity, closeOwner func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if closeOwner != nil {
		if err := closeOwner(); err != nil {
			c.pending = closeOwner
		}
	}
	// Record the unresolved owner BEFORE relinquishing the old pipe lease.
	c.lease.Release(identity)
}

func (c *clientLifecycle) retryLocked() error {
	if c.pending == nil {
		return nil
	}
	if err := c.pending(); err != nil {
		return errors.Join(errCleanupPending, err)
	}
	c.pending = nil
	return nil
}

func (c *clientLifecycle) retryCleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retryLocked()
}

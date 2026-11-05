package control

import (
	"bluegrass/native/core/clock"
	"bluegrass/native/core/gate"
	"strconv"
	"time"
)

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	x := *t
	return &x
}
func cloneSnapshot(s NativeSnapshot) NativeSnapshot {
	if s.Context != nil {
		x := *s.Context
		s.Context = &x
	}
	s.ValidUntilUTC = cloneTime(s.ValidUntilUTC)
	return s
}
func cloneAck(a CommandAck) CommandAck { a.Snapshot = cloneSnapshot(a.Snapshot); return a }
func (c *Controller) Snapshot() NativeSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readLocked()
	return cloneSnapshot(c.snap)
}

// PublishObservedExit accepts a trusted host's validated diagnostic observation,
// never an engine cache. Identity, gate and lease checks are atomic with publication;
// it neither opens forwarding nor changes protection on rejection.
func (c *Controller) PublishObservedExit(expected ControlContext, ipv4 string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.active == nil || !c.currentLocked(c.active) || c.snap.Context == nil || *c.snap.Context != expected ||
		c.snap.Connection != "connected" || !c.snap.GateKnown || !c.snap.BusinessGateOpen || c.snap.Expired || !gate.PublicIPv4(ipv4) {
		return false
	}
	now := c.readLocked().EffectiveUTC
	if c.clockInvalid || gate.Expired(now, c.snap.ValidUntilUTC) {
		return false
	}
	c.snap.VerifiedExitIPv4 = ipv4
	c.snap.EventSequence++
	return true
}

func (c *Controller) ack(req ControlRequest, disposition, code string) CommandAck {
	return CommandAck{RequestID: req.RequestID, Disposition: disposition, ErrorCode: code, Snapshot: cloneSnapshot(c.snap)}
}
func (c *Controller) remember(r ControlRequest, digest [32]byte, a CommandAck, err error) {
	// FIFO admission order bounds both the map and its eviction metadata. Retries
	// do not refresh retention, so repeated requests cannot pin receipts forever.
	if len(c.receiptOrder) < maxHistory {
		c.receiptOrder = append(c.receiptOrder, r.RequestID)
	} else {
		oldID := c.receiptOrder[c.receiptNext]
		old := c.receipts[oldID]
		// Validation failures do not consume lastSequence. Preserve a separate
		// floor so forgetting their receipts cannot cause them to be reevaluated.
		if old.sequence > c.evictedSequence {
			c.evictedSequence = old.sequence
		}
		delete(c.receipts, oldID)
		c.receiptOrder[c.receiptNext] = r.RequestID
		c.receiptNext = (c.receiptNext + 1) % maxHistory
	}
	c.receipts[r.RequestID] = receipt{digest: digest, ack: cloneAck(a), err: err, sequence: r.Sequence}
}
func (c *Controller) readLocked() clock.TimeReading {
	reading := c.clk.Read()
	prior := c.snap.Time
	if reading.RuntimeEpoch != c.runtime || reading.MonotonicNanos < prior.MonotonicNanos || reading.EffectiveUTC.IsZero() {
		c.clockInvalid = true
		c.snap.ErrorCode = "INVALID_CLOCK"
		return prior
	}
	floor := prior.EffectiveUTC.Add(time.Duration(reading.MonotonicNanos - prior.MonotonicNanos))
	if reading.EffectiveUTC.Before(floor) {
		reading.EffectiveUTC = floor
	}
	c.snap.Time = reading
	return reading
}
func decimal(value string) (uint64, bool) {
	if value == "" || len(value) > 20 || len(value) > 1 && value[0] == '0' {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}

// AcceptTraffic has a separate sequence from control events. Decimal cumulative
// counters never round-trip through floating point.
func (c *Controller) AcceptTraffic(s TrafficSnapshot) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.snap.Connection != "connected" || c.active == nil || c.snap.Context == nil || *c.snap.Context != s.Context || s.CounterEpoch == "" || len(s.CounterEpoch) > 128 || s.Sequence == 0 || s.MonotonicNanos < 0 {
		return false
	}
	up, ok := decimal(s.UploadBytes)
	if !ok {
		return false
	}
	down, ok := decimal(s.DownloadBytes)
	if !ok {
		return false
	}
	old := c.snap.Traffic
	if old.CounterEpoch != "" {
		if s.Sequence <= old.Sequence || s.MonotonicNanos <= old.MonotonicNanos {
			return false
		}
		if old.CounterEpoch == s.CounterEpoch {
			ou, _ := decimal(old.UploadBytes)
			od, _ := decimal(old.DownloadBytes)
			if up < ou || down < od {
				return false
			}
		} else if c.counterEpochs[s.CounterEpoch] {
			return false
		}
		if old.CounterEpoch != s.CounterEpoch || s.Sequence != old.Sequence+1 {
			s.Complete = false
		}
	}
	if !s.Available {
		s.Complete = false
	}
	if len(c.counterEpochs) >= maxHistory && !c.counterEpochs[s.CounterEpoch] {
		return false
	}
	c.counterEpochs[s.CounterEpoch] = true
	c.snap.Traffic = s
	return true
}

package clock

import (
	"errors"
	"sync"
	"time"
)

type Confidence string

const (
	Local  Confidence = "local_estimate"
	Server Confidence = "server_estimate"
)

type TimeReading struct {
	EffectiveUTC   time.Time  `json:"effectiveUtc"`
	MonotonicNanos int64      `json:"monotonicNanos"`
	RuntimeEpoch   string     `json:"runtimeEpoch"`
	Confidence     Confidence `json:"confidence"`
}
type Clock interface{ Read() TimeReading }

type Monotonic struct {
	mu         sync.Mutex
	start      time.Time
	anchor     time.Time
	epoch      string
	confidence Confidence
}

// New carries an explicitly supplied UTC estimate; it does not claim fresh server calibration.
func New(epoch string, anchor time.Time, confidence Confidence) *Monotonic {
	return &Monotonic{start: time.Now(), anchor: anchor.UTC(), epoch: epoch, confidence: confidence}
}
func (c *Monotonic) Read() TimeReading {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.start)
	return TimeReading{c.anchor.Add(elapsed), int64(elapsed), c.epoch, c.confidence}
}

// Calibrate accepts the trusted UI adapter's fresh HTTPS server anchor. The
// native monotonic counter never resets, including a later calibration.
func (c *Monotonic) Calibrate(serverUTC time.Time) error {
	if serverUTC.IsZero() {
		return errors.New("INVALID_CLOCK")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.start)
	anchor := serverUTC.UTC().Add(-elapsed)
	if c.confidence == Server && anchor.Before(c.anchor) {
		return nil
	}
	c.anchor = anchor
	c.confidence = Server
	return nil
}

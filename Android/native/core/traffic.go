package bridge

import (
	"encoding/json"
	"io"
	"math"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
)

// Only application payloads on the selected proxy path enter this counter.
// Holding the read lock across IO lets Close freeze a final, quiescent sample
// after the lease has closed all transports; no per-destination state is kept.
type trafficCounters struct {
	mu               sync.RWMutex
	stopped          bool
	upload, download atomic.Uint64
	overflow         atomic.Bool
}

func (t *trafficCounters) add(counter *atomic.Uint64, n int) {
	if n <= 0 {
		return
	}
	for {
		old := counter.Load()
		next := old + uint64(n)
		if uint64(n) > math.MaxUint64-old {
			next = math.MaxUint64
			t.overflow.Store(true)
		}
		if counter.CompareAndSwap(old, next) {
			return
		}
	}
}
func (t *trafficCounters) freeze() { t.mu.Lock(); t.stopped = true; t.mu.Unlock() }

type trafficConn struct {
	net.Conn
	counters *trafficCounters
}

func (c *trafficConn) Read(p []byte) (int, error) {
	c.counters.mu.RLock()
	defer c.counters.mu.RUnlock()
	if c.counters.stopped {
		return 0, io.ErrClosedPipe
	}
	n, err := c.Conn.Read(p)
	c.counters.add(&c.counters.download, n)
	return n, err
}
func (c *trafficConn) Write(p []byte) (int, error) {
	c.counters.mu.RLock()
	defer c.counters.mu.RUnlock()
	if c.counters.stopped {
		return 0, io.ErrClosedPipe
	}
	n, err := c.Conn.Write(p)
	c.counters.add(&c.counters.upload, n)
	return n, err
}

// Traffic remains readable after Close so Android can checkpoint a core before
// replacing it during a physical-network reconnect. Decimal strings cross JNI.
func (s *Session) Traffic() string {
	value := struct {
		Upload   string `json:"uploadBytes"`
		Download string `json:"downloadBytes"`
		Complete bool   `json:"complete"`
	}{strconv.FormatUint(s.traffic.upload.Load(), 10), strconv.FormatUint(s.traffic.download.Load(), 10), !s.traffic.overflow.Load()}
	data, _ := json.Marshal(value)
	return string(data)
}

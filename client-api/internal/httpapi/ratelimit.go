package httpapi

import (
	"container/heap"
	"sync"
	"time"
)

type rateEntry struct {
	key     string
	count   int
	expires time.Time
}

type expiryHeap []*rateEntry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].expires.Before(h[j].expires) }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(value any)    { *h = append(*h, value.(*rateEntry)) }
func (h *expiryHeap) Pop() any {
	old := *h
	last := len(old) - 1
	entry := old[last]
	old[last] = nil
	*h = old[:last]
	return entry
}

type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	maxKeys int
	entries map[string]*rateEntry
	expiry  expiryHeap
}

func newRateLimiter(limit, maxKeys int) *rateLimiter {
	return &rateLimiter{limit: limit, maxKeys: maxKeys, entries: make(map[string]*rateEntry)}
}

func (l *rateLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	// Exactly one heap record per live key; attempts never extend its window or
	// append stale records. Both data structures remain bounded by maxKeys.
	for len(l.expiry) > 0 && !l.expiry[0].expires.After(now) {
		expired := heap.Pop(&l.expiry).(*rateEntry)
		delete(l.entries, expired.key)
	}
	if entry, exists := l.entries[key]; exists {
		if entry.count >= l.limit {
			return false, retrySeconds(entry.expires.Sub(now))
		}
		entry.count++
		return true, 0
	}
	if len(l.entries) >= l.maxKeys {
		return false, retrySeconds(l.expiry[0].expires.Sub(now))
	}
	entry := &rateEntry{key: key, count: 1, expires: now.Add(time.Minute)}
	l.entries[key] = entry
	heap.Push(&l.expiry, entry)
	return true, 0
}

func retrySeconds(wait time.Duration) int {
	seconds := int((wait + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

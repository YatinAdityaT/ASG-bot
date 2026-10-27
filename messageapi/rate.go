package messageapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	rateLimit  = 30
	rateWindow = time.Minute
)

type rateEntry struct {
	windowStart time.Time
	count       int
}

type fixedWindowLimiter struct {
	mu          sync.Mutex
	entries     map[string]rateEntry
	now         func() time.Time
	nextCleanup time.Time
}

func newFixedWindowLimiter(now func() time.Time) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		entries:     make(map[string]rateEntry),
		now:         now,
		nextCleanup: now().Add(rateWindow),
	}
}

func (limiter *fixedWindowLimiter) allow(request *http.Request) bool {
	address := request.RemoteAddr
	if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		address = host
	}

	now := limiter.now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if !now.Before(limiter.nextCleanup) {
		for key, candidate := range limiter.entries {
			if !now.Before(candidate.windowStart.Add(rateWindow)) {
				delete(limiter.entries, key)
			}
		}
		limiter.nextCleanup = now.Add(rateWindow)
	}

	entry := limiter.entries[address]
	if entry.windowStart.IsZero() || !now.Before(entry.windowStart.Add(rateWindow)) {
		limiter.entries[address] = rateEntry{windowStart: now, count: 1}
		return true
	}
	if entry.count >= rateLimit {
		return false
	}
	entry.count++
	limiter.entries[address] = entry
	return true
}

package messageapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestFixedWindowLimiterEvictsExpiredAddresses(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	limiter := newFixedWindowLimiter(func() time.Time { return now })

	first := httptest.NewRequest("GET", "/healthz", nil)
	first.RemoteAddr = "192.0.2.1:1234"
	if !limiter.allow(first) {
		t.Fatal("first address unexpectedly rejected")
	}

	now = now.Add(rateWindow)
	second := httptest.NewRequest("GET", "/healthz", nil)
	second.RemoteAddr = "192.0.2.2:1234"
	if !limiter.allow(second) {
		t.Fatal("second address unexpectedly rejected")
	}

	if len(limiter.entries) != 1 {
		t.Fatalf("entry count = %d, want 1 after stale eviction", len(limiter.entries))
	}
	if _, exists := limiter.entries["192.0.2.1"]; exists {
		t.Fatal("expired address remained in limiter")
	}
}

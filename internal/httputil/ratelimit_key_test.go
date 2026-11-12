package httputil

import (
	"fmt"
	"testing"
)

func TestRateLimitKeyGroupsIPv6By64(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.5":          "203.0.113.5",
		"::ffff:203.0.113.5":   "203.0.113.5",
		"2001:db8:1:2::1":      "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::9": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":      "2001:db8:1:3::/64",
		"not-an-ip":            "not-an-ip",
	} {
		if got := RateLimitKey(in); got != want {
			t.Errorf("RateLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Rotating through many addresses can neither grow limiter memory without
// bound nor obtain a fresh budget once the table is full.
func TestRateLimiterBoundsVisitors(t *testing.T) {
	rl := NewRateLimiter(0.001, 1)
	defer rl.Stop()
	for i := 0; i < maxRateLimitVisitors+500; i++ {
		rl.Allow(fmt.Sprintf("10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff))
	}
	rl.mu.Lock()
	n := len(rl.visitors)
	rl.mu.Unlock()
	if n > maxRateLimitVisitors+1 {
		t.Fatalf("limiter tracks %d visitors, cap is %d", n, maxRateLimitVisitors)
	}
	if rl.Allow("192.0.2.200") {
		t.Fatal("a new address got a fresh budget after the table filled")
	}
}

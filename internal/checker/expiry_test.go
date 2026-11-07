package checker

import (
	"testing"
	"time"
)

func TestExpiryInstantBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		status    string
		days      int
	}{
		{"just expired", -time.Nanosecond, "down", 0},
		{"exact expiry", 0, "down", 0},
		{"just valid", time.Nanosecond, "degraded", 0},
		{"last day", 24*time.Hour - time.Nanosecond, "degraded", 0},
		{"warning threshold", 30 * 24 * time.Hour, "degraded", 30},
		{"outside warning", 31 * 24 * time.Hour, "up", 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, days := expiryStatus(now.Add(tc.remaining), now, 30)
			if status != tc.status || days != tc.days {
				t.Fatalf("status=%s,days=%d; want %s,%d", status, days, tc.status, tc.days)
			}
		})
	}
}

func TestDomainExpiryLastDay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		status    string
	}{
		{"twenty three hours", 23*time.Hour + 59*time.Minute, "degraded"},
		{"twelve hours", 12 * time.Hour, "degraded"},
		{"one minute", time.Minute, "degraded"},
		{"expired", -time.Minute, "down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expiry := time.Now().UTC().Add(tc.remaining)
			got, err := evaluateDomainExpiry("Registry Expiry Date: "+expiry.Format(time.RFC3339), 10, 30)
			if err != nil || got.Status != tc.status {
				t.Fatalf("result=%+v,error=%v; want %s", got, err, tc.status)
			}
		})
	}
}

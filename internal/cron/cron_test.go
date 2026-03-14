package cron

import (
	"testing"
	"time"
)

func TestParseAndMatch(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		time  string
		match bool
	}{
		{"every minute", "* * * * *", "2026-01-15T10:30:00Z", true},
		{"specific minute", "30 * * * *", "2026-01-15T10:30:00Z", true},
		{"wrong minute", "15 * * * *", "2026-01-15T10:30:00Z", false},
		{"hour and minute", "0 2 * * *", "2026-01-15T02:00:00Z", true},
		{"hour and minute miss", "0 2 * * *", "2026-01-15T03:00:00Z", false},
		{"weekday", "0 0 * * 1", "2026-01-19T00:00:00Z", true},       // Monday
		{"weekday miss", "0 0 * * 1", "2026-01-20T00:00:00Z", false}, // Tuesday
		{"dom", "0 0 15 * *", "2026-01-15T00:00:00Z", true},
		{"dom miss", "0 0 15 * *", "2026-01-16T00:00:00Z", false},
		{"month", "0 0 1 3 *", "2026-03-01T00:00:00Z", true},
		{"month miss", "0 0 1 3 *", "2026-04-01T00:00:00Z", false},
		{"range", "0-5 * * * *", "2026-01-15T10:03:00Z", true},
		{"range miss", "0-5 * * * *", "2026-01-15T10:06:00Z", false},
		{"step", "*/15 * * * *", "2026-01-15T10:30:00Z", true},
		{"step miss", "*/15 * * * *", "2026-01-15T10:31:00Z", false},
		{"list", "0,15,30,45 * * * *", "2026-01-15T10:45:00Z", true},
		{"list miss", "0,15,30,45 * * * *", "2026-01-15T10:20:00Z", false},
		{"complex", "30 2 * * 1-5", "2026-01-20T02:30:00Z", true},               // Tuesday
		{"complex miss weekend", "30 2 * * 1-5", "2026-01-18T02:30:00Z", false}, // Sunday
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := Parse(tt.expr)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.expr, err)
			}
			ts, _ := time.Parse(time.RFC3339, tt.time)
			got := expr.Matches(ts)
			if got != tt.match {
				t.Fatalf("Matches(%s) = %v, want %v", tt.time, got, tt.match)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"",
		"* * *",
		"* * * * * *",
		"60 * * * *",
		"-1 * * * *",
		"* 24 * * *",
		"* * 0 * *",
		"* * * 13 *",
		"* * * * 7",
		"abc * * * *",
		"*/0 * * * *",
	}
	for _, expr := range bad {
		t.Run(expr, func(t *testing.T) {
			_, err := Parse(expr)
			if err == nil {
				t.Fatalf("expected error for %q", expr)
			}
		})
	}
}

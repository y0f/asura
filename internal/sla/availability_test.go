package sla

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestSLAEmptyHistoryAndCurrentPassiveType(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "sla.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	m := &storage.Monitor{Name: "state", Type: "http", Target: "https://example.test", SLATarget: 99.9, Enabled: true, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	status, err := Compute(ctx, store, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMissing := func(value any, keys ...string) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got["has_data"] != false {
			t.Fatalf("missing observations marked available: %s", encoded)
		}
		for _, key := range keys {
			if value, present := got[key]; !present || value != nil {
				t.Errorf("%s must be explicit null: %s", key, encoded)
			}
		}
	}
	assertMissing(status, "uptime_pct_month", "uptime_pct_30d", "budget_used_secs", "budget_remain_secs", "budget_remain_pct", "budget_at_risk", "breached")
	now := time.Now().UTC()
	entries, err := ComputeReport(ctx, store, now.Year(), now.Month())
	if err != nil || len(entries) != 1 {
		t.Fatalf("empty monthly report: %d %v", len(entries), err)
	}
	assertMissing(entries[0], "uptime_pct", "used_secs", "remain_secs", "budget_remain_pct", "budget_at_risk", "breached")
	// Current storage state wins even when the caller already loaded active config.
	m.Type = "manual"
	if err := store.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Compute(ctx, store, m.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("current passive type treated as sampled: %v", err)
	}
	entries, err = ComputeReport(ctx, store, now.Year(), now.Month())
	if err != nil || len(entries) != 0 {
		t.Fatalf("passive target in monthly report: %d %v", len(entries), err)
	}
}

func TestSLAMeasuredZeroAndIndependentRollingAvailability(t *testing.T) {
	for _, value := range []any{
		Status{TotalChecks: 1, TotalChecks30d: 0, UptimePctMonth: 0, UptimePct30d: 100, Breached: true},
		ReportEntry{TotalChecks: 1, UptimePct: 0, Breached: true},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		key := "uptime_pct_month"
		if _, ok := value.(ReportEntry); ok {
			key = "uptime_pct"
		}
		if got["has_data"] != true || got[key] != float64(0) || got["breached"] != true {
			t.Fatalf("measured zero lost: %s", encoded)
		}
		if _, ok := value.(Status); ok && got["uptime_pct_30d"] != nil {
			t.Fatalf("missing rolling samples fabricated: %s", encoded)
		}
	}
}

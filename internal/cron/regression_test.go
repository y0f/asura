package cron_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/y0f/asura/internal/cron"
	"github.com/y0f/asura/internal/storage"
)

// Standard (Vixie/POSIX) cron: when both day-of-month and day-of-week are
// restricted, a time matches if EITHER field matches.
func TestDomDowOrSemantics(t *testing.T) {
	expr, err := cron.Parse("0 2 1 * 0")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-08 is a Sunday, not the 1st.
	sunday := time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC)
	if sunday.Weekday() != time.Sunday {
		t.Fatalf("setup: %v is not Sunday", sunday)
	}
	if !expr.Matches(sunday) {
		t.Errorf("\"0 2 1 * 0\" should match Sunday %s (dow OR dom), got false", sunday)
	}
	// 2026-12-01 is a Tuesday, the 1st.
	first := time.Date(2026, 12, 1, 2, 0, 0, 0, time.UTC)
	if !expr.Matches(first) {
		t.Errorf("\"0 2 1 * 0\" should match the 1st %s (dow OR dom), got false", first)
	}
	// 2026-11-09 is a Monday, neither the 1st nor a Sunday.
	if expr.Matches(time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)) {
		t.Error("\"0 2 1 * 0\" matched a Monday that is not the 1st")
	}

	// With one day field unrestricted, the other alone decides.
	sundays, err := cron.Parse("0 2 * * 0")
	if err != nil {
		t.Fatal(err)
	}
	if sundays.Matches(first) {
		t.Errorf("\"0 2 * * 0\" matched Tuesday %s", first)
	}
	if !sundays.Matches(sunday) {
		t.Errorf("\"0 2 * * 0\" did not match Sunday %s", sunday)
	}
}

// A cron maintenance window of 60 minutes starting at 02:00 must not still be
// active at 03:00:30.
func TestCronMaintenanceWindowEndsOnTime(t *testing.T) {
	f, err := os.CreateTemp("", "asura-cron-regression-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	store, err := storage.NewSQLiteStore(f.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	start := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	mw := &storage.MaintenanceWindow{
		Name:      "nightly",
		StartTime: start,
		EndTime:   start.Add(60 * time.Minute),
		Recurring: "cron",
		CronExpr:  "0 2 * * *",
	}
	if err := store.CreateMaintenanceWindow(ctx, mw); err != nil {
		t.Fatal(err)
	}
	inside := time.Date(2026, 11, 3, 2, 59, 0, 0, time.UTC)
	if ok, err := store.IsMonitorInMaintenance(ctx, 1, inside); err != nil || !ok {
		t.Fatalf("setup: 02:59 should be in window, got %v %v", ok, err)
	}
	after := time.Date(2026, 11, 3, 3, 0, 30, 0, time.UTC)
	ok, err := store.IsMonitorInMaintenance(ctx, 1, after)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("60-minute window starting 02:00 still active at %s", after.Format("15:04:05"))
	}
}

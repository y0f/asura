package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestMonitor(tb testing.TB, s *SQLiteStore, name string) *Monitor {
	tb.Helper()
	m := &Monitor{Name: name, Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := s.CreateMonitor(context.Background(), m); err != nil {
		tb.Fatal(err)
	}
	return m
}

func benchStore(tb testing.TB) *SQLiteStore {
	tb.Helper()
	f, err := os.CreateTemp("", "asura-bench-*.db")
	if err != nil {
		tb.Fatal(err)
	}
	f.Close()
	tb.Cleanup(func() { os.Remove(f.Name()); os.Remove(f.Name() + "-wal"); os.Remove(f.Name() + "-shm") })
	s, err := NewSQLiteStore(f.Name(), 4)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	return s
}

// seedChecks bulk-inserts check_results for monitorID at the given times.
// Row i gets response_time 100+i%50.
func seedChecks(tb testing.TB, s *SQLiteStore, monitorID int64, times []time.Time, status func(i int) string) {
	tb.Helper()
	tx, err := s.writeDB.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO check_results (monitor_id, status, response_time, created_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	for i, ts := range times {
		if _, err := stmt.Exec(monitorID, status(i), 100+i%50, formatTime(ts)); err != nil {
			tb.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Rows with a NULL monitor_id never conflict on the UNIQUE key, so a re-run of
// the rollup must not add a duplicate row for them.
func TestRollupRequestLogsIdempotentForNullMonitor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	date := "2025-01-15"
	ts, _ := time.Parse(timeFormat, date+"T12:00:00Z")
	if err := s.InsertRequestLogBatch(ctx, []*RequestLog{
		{Method: "GET", Path: "/", StatusCode: 200, LatencyMs: 10, ClientIP: "a", RouteGroup: "web", CreatedAt: ts},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RollupRequestLogs(ctx, date); err != nil {
			t.Fatal(err)
		}
	}
	var n, requests int
	if err := s.readDB.QueryRow("SELECT COUNT(*), COALESCE(SUM(requests),0) FROM request_log_rollups WHERE date=?", date).Scan(&n, &requests); err != nil {
		t.Fatal(err)
	}
	if n != 1 || requests != 1 {
		t.Fatalf("expected 1 rollup row with 1 request after 3 rollups, got %d rows, %d requests", n, requests)
	}
}

func TestRecurringMaintenanceAcrossMidnight(t *testing.T) {
	sun := time.Date(2025, 1, 5, 23, 0, 0, 0, time.UTC) // Sunday 23:00
	tenth := time.Date(2025, 1, 10, 23, 0, 0, 0, time.UTC)
	weekly := &MaintenanceWindow{Recurring: "weekly", StartTime: sun, EndTime: sun.Add(2 * time.Hour)}
	monthly := &MaintenanceWindow{Recurring: "monthly", StartTime: tenth, EndTime: tenth.Add(2 * time.Hour)}
	daily := &MaintenanceWindow{Recurring: "daily", StartTime: sun, EndTime: sun.Add(2 * time.Hour)}
	tests := []struct {
		name string
		mw   *MaintenanceWindow
		at   time.Time
		want bool
	}{
		{"weekly before midnight", weekly, time.Date(2025, 1, 12, 23, 30, 0, 0, time.UTC), true},
		{"weekly after midnight", weekly, time.Date(2025, 1, 13, 0, 30, 0, 0, time.UTC), true},
		{"weekly after end", weekly, time.Date(2025, 1, 13, 1, 30, 0, 0, time.UTC), false},
		{"weekly wrong day", weekly, time.Date(2025, 1, 14, 0, 30, 0, 0, time.UTC), false},
		{"monthly after midnight", monthly, time.Date(2025, 2, 11, 0, 30, 0, 0, time.UTC), true},
		{"monthly wrong day", monthly, time.Date(2025, 2, 12, 0, 30, 0, 0, time.UTC), false},
		{"daily after midnight", daily, time.Date(2025, 3, 3, 0, 59, 0, 0, time.UTC), true},
		{"daily outside", daily, time.Date(2025, 3, 3, 12, 0, 0, 0, time.UTC), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInWindow(tt.mw, tt.at); got != tt.want {
				t.Fatalf("isInWindow(%s) = %v, want %v", tt.at, got, tt.want)
			}
		})
	}
}

// Windows are stored in UTC; callers pass time.Now() in the local zone.
func TestRecurringMaintenanceLocalZone(t *testing.T) {
	start := time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC)
	mw := &MaintenanceWindow{Recurring: "daily", StartTime: start, EndTime: start.Add(time.Hour)}
	cet := time.FixedZone("CET", 3600)
	at := time.Date(2025, 3, 3, 2, 30, 0, 0, time.UTC).In(cet)
	if !isInWindow(mw, at) {
		t.Fatal("02:30 UTC (expressed as CET) should be inside the 02:00-03:00 UTC daily window")
	}
	if isInWindow(mw, time.Date(2025, 3, 3, 2, 30, 0, 0, cet)) {
		t.Fatal("02:30 CET (01:30 UTC) should be outside the 02:00-03:00 UTC daily window")
	}

	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateMaintenanceWindow(ctx, mw); err != nil {
		t.Fatal(err)
	}
	in, err := s.IsMonitorInMaintenance(ctx, 1, at)
	if err != nil {
		t.Fatal(err)
	}
	if !in {
		t.Fatal("IsMonitorInMaintenance: 02:30 UTC given in CET should be in the 02:00-03:00 UTC window")
	}
}

// GetUptimePercent combines daily rollups with raw rows for the uncovered
// edges and must give the same answer as a raw scan.
func TestGetUptimePercentUsesRollups(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "u")
	ctx := context.Background()
	now := time.Now().UTC()
	var times []time.Time
	for i := 0; i < 5*24*6; i++ { // every 10 minutes for 5 days
		times = append(times, now.Add(-time.Duration(i)*10*time.Minute))
	}
	seedChecks(t, s, m.ID, times, func(i int) string {
		if i%7 == 0 {
			return "down"
		}
		return "up"
	})
	from := now.Add(-4*24*time.Hour - 3*time.Hour)

	want, err := s.GetUptimePercent(ctx, m.ID, from, now) // no rollups yet: raw scan
	if err != nil {
		t.Fatal(err)
	}
	// Roll up some of the days so both the rollup and raw paths are used.
	today := now.Truncate(24 * time.Hour)
	for d := 2; d <= 4; d++ {
		day := today.AddDate(0, 0, -d)
		for h := 0; h < 24; h++ {
			if err := s.RollupHourly(ctx, day.Add(time.Duration(h)*time.Hour).Format("2006-01-02T15")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetUptimePercent(ctx, m.ID, from, now)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("uptime with rollups = %v, raw scan = %v", got, want)
	}

	// A rolled-up day must be served from check_result_daily, not raw rows.
	mid := today.AddDate(0, 0, -3)
	if _, err := s.writeDB.Exec(`DELETE FROM check_results WHERE created_at >= ? AND created_at < ?`,
		formatTime(mid), formatTime(mid.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetUptimePercent(ctx, m.ID, from, now)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("rolled-up day not served from rollup: got %v, want %v", got, want)
	}
}

func TestGetMonitorSparklinesNewestN(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := newTestMonitor(t, s, "a")
	b := newTestMonitor(t, s, "b")
	var times []time.Time
	for i := 0; i < 30; i++ {
		times = append(times, now.Add(-time.Duration(i)*time.Minute)) // i=0 newest
	}
	seedChecks(t, s, a.ID, times, func(int) string { return "up" })
	got, err := s.GetMonitorSparklines(ctx, []int64{a.ID, b.ID}, 20)
	if err != nil {
		t.Fatal(err)
	}
	pts := got[a.ID]
	if len(pts) != 20 {
		t.Fatalf("expected 20 points, got %d", len(pts))
	}
	// The newest 20 rows have response_time 100..119 (newest first), so oldest-first is 119..100.
	for k, p := range pts {
		if want := int64(119 - k); p.ResponseTime != want {
			t.Fatalf("point %d: response_time %d, want %d (oldest first)", k, p.ResponseTime, want)
		}
	}
	if _, ok := got[b.ID]; ok {
		t.Fatal("monitor without checks should have no entry")
	}
}

func BenchmarkGetUptimePercent90d(b *testing.B) {
	s := benchStore(b)
	m := newTestMonitor(b, s, "u")
	now := time.Now().UTC()
	const n = 130000
	times := make([]time.Time, n)
	for i := range times {
		times[i] = now.Add(-time.Duration(i) * time.Minute)
	}
	seedChecks(b, s, m.ID, times, func(i int) string {
		if i%100 == 0 {
			return "down"
		}
		return "up"
	})
	ctx := context.Background()
	NewRetentionWorker(s, 90, 7, time.Hour, discardLogger()).rollup(ctx)
	from := now.Add(-90 * 24 * time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GetUptimePercent(ctx, m.ID, from, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetMonitorSparklines(b *testing.B) {
	s := benchStore(b)
	const monitors, rows = 10, 20000
	now := time.Now().UTC()
	var ids []int64
	for mi := 0; mi < monitors; mi++ {
		m := newTestMonitor(b, s, fmt.Sprintf("m%d", mi))
		ids = append(ids, m.ID)
		times := make([]time.Time, rows)
		for i := range times {
			times[i] = now.Add(-time.Duration(i) * time.Minute)
		}
		seedChecks(b, s, m.ID, times, func(int) string { return "up" })
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GetMonitorSparklines(ctx, ids, 20); err != nil {
			b.Fatal(err)
		}
	}
}

// With retention_days=1 the oldest re-rolled hour is partly purged; its stored
// rollup must not be replaced by partial counts.
func TestRetentionDoesNotRerollPurgedHour(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "r")
	ctx := context.Background()
	now := time.Now().UTC()
	boundary := now.Add(-24 * time.Hour).Truncate(time.Hour) // start of hour h=24
	if now.Add(-24*time.Hour).Sub(boundary) < 2*time.Minute {
		t.Skip("too close to the top of the hour for a deterministic fixture")
	}
	var times []time.Time
	for i := 0; i < 60; i++ {
		times = append(times, boundary.Add(time.Duration(i)*time.Minute+30*time.Second))
	}
	seedChecks(t, s, m.ID, times, func(int) string { return "up" })
	hour := boundary.Format("2006-01-02T15")
	if err := s.RollupHourly(ctx, hour); err != nil {
		t.Fatal(err)
	}

	w := NewRetentionWorker(s, 1, 7, time.Hour, discardLogger())
	w.purge(ctx)
	w.rollup(ctx)

	var after int
	if err := s.readDB.QueryRow(`SELECT total FROM check_result_hourly WHERE monitor_id=? AND hour=?`, m.ID, hour).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 60 {
		t.Fatalf("hour %s rollup total changed from 60 to %d after purge+rollup", hour, after)
	}
}

// PurgeOldData deletes in batches so other writers are not blocked for the
// whole purge, and content_changes has an index to purge by.
func TestPurgeOldDataDoesNotBlockWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	s := testStore(t)
	m := newTestMonitor(t, s, "p")
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -200)
	const n = 400000
	times := make([]time.Time, n)
	for i := range times {
		times[i] = old.Add(time.Duration(i) * time.Second)
	}
	seedChecks(t, s, m.ID, times, func(int) string { return "up" })

	var wg sync.WaitGroup
	var purgeDur time.Duration
	var deleted int64
	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		t0 := time.Now()
		var err error
		if deleted, err = s.PurgeOldData(ctx, time.Now().AddDate(0, 0, -30)); err != nil {
			t.Error(err)
		}
		purgeDur = time.Since(t0)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	t0 := time.Now()
	if err := s.InsertCheckResult(ctx, &CheckResult{MonitorID: m.ID, Status: "up"}); err != nil {
		t.Fatal(err)
	}
	insertLatency := time.Since(t0)
	wg.Wait()
	t.Logf("purge of %d rows took %v; concurrent InsertCheckResult waited %v", n, purgeDur, insertLatency)

	if deleted < n {
		t.Fatalf("expected at least %d deleted rows, got %d", n, deleted)
	}
	if insertLatency > 200*time.Millisecond && insertLatency > purgeDur/2 {
		t.Fatalf("InsertCheckResult blocked %v behind the purge (%v)", insertLatency, purgeDur)
	}

	var plan strings.Builder
	r, err := s.readDB.Query(`EXPLAIN QUERY PLAN DELETE FROM content_changes WHERE created_at < '2020-01-01T00:00:00Z'`)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var a, b, c int
		var d string
		r.Scan(&a, &b, &c, &d)
		plan.WriteString(d + "; ")
	}
	r.Close()
	if !strings.Contains(plan.String(), "idx_content_changes_created_at") {
		t.Fatalf("content_changes purge does not use the created_at index: %s", plan.String())
	}
}

// After the worker was down for more than a day, the next rollup must fill in
// the hours and days it missed.
func TestRollupCatchesUpAfterDowntime(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "g")
	ctx := context.Background()
	now := time.Now().UTC()
	day3 := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC).AddDate(0, 0, -3)
	var times []time.Time
	for i := 0; i < 60; i++ {
		times = append(times, day3.Add(time.Duration(i)*time.Minute))
	}
	seedChecks(t, s, m.ID, times, func(int) string { return "up" })

	NewRetentionWorker(s, 90, 7, time.Hour, discardLogger()).rollup(ctx)

	var total int
	if err := s.readDB.QueryRow(`SELECT COALESCE(SUM(total),0) FROM check_result_daily WHERE monitor_id=? AND day=?`,
		m.ID, day3.Format("2006-01-02")).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 60 {
		t.Fatalf("daily rollup for %s has total %d, want 60", day3.Format("2006-01-02"), total)
	}
}

// A failure partway through the legacy token migration must leave every token
// untouched, so the retry hashes each exactly once.
func TestHashLegacyTokensAtomic(t *testing.T) {
	f, err := os.CreateTemp("", "asura-hash-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path); os.Remove(path + "-wal"); os.Remove(path + "-shm") })

	s, err := NewSQLiteStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustExec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a legacy DB: plaintext tokens and no tokens_hashed flag.
	mustExec(s.writeDB, `INSERT INTO agents (name, token) VALUES ('a1', 'plain-1'), ('a2', 'plain-2')`)
	mustExec(s.writeDB, `DELETE FROM meta WHERE key='tokens_hashed'`)
	// Fail the 2nd UPDATE.
	mustExec(s.writeDB, `CREATE TRIGGER boom BEFORE UPDATE OF token ON agents WHEN OLD.name='a2' BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	s.Close()

	if _, err := NewSQLiteStore(path, 1); err == nil {
		t.Fatal("expected injected failure on first restart")
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(raw, `DROP TRIGGER boom`)
	raw.Close()

	s2, err := NewSQLiteStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	for name, plain := range map[string]string{"a1": "plain-1", "a2": "plain-2"} {
		var tok string
		if err := s2.readDB.QueryRow(`SELECT token FROM agents WHERE name=?`, name).Scan(&tok); err != nil {
			t.Fatal(err)
		}
		if tok != sha256Hex(plain) {
			t.Fatalf("agent %s token = %q, want sha256(%q) (double-hashed: %v)", name, tok, plain, tok == sha256Hex(sha256Hex(plain)))
		}
	}
}

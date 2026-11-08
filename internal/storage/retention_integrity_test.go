package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failedRollupStore struct {
	Store
	purged bool
}

func (s *failedRollupStore) RollupPending(context.Context, time.Time) error {
	return errors.New("injected rollup failure")
}

func (s *failedRollupStore) PurgeOldData(context.Context, time.Time) (int64, error) {
	s.purged = true
	return 0, errors.New("unexpected purge")
}

func TestRetentionWorkerSkipsPurgeOnRollupFailure(t *testing.T) {
	s := &failedRollupStore{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NewRetentionWorker(s, 7, 7, time.Hour, discardLogger()).Run(ctx)
	if s.purged {
		t.Fatal("worker purged source history after rollup failed")
	}
}

func TestRollupCheckpointHandlesOpenHoursLateRowsAndRetry(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "incremental")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	old := now.Truncate(24*time.Hour).AddDate(0, 0, -2)
	insertRollupCheck(t, s, m.ID, old, "up", 100)
	insertRollupCheck(t, s, m.ID, now, "up", 100)
	if err := s.RollupPending(ctx, now); err != nil {
		t.Fatal(err)
	}
	var open int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM check_result_hourly WHERE hour=?`, now.Format("2006-01-02T15")).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Fatal("current hour was summarized before closing")
	}
	insertRollupCheck(t, s, m.ID, old, "down", 200)
	if err := s.RollupPending(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var total, up int
	if err := s.readDB.QueryRow(`SELECT total, up_count FROM check_result_daily WHERE monitor_id=? AND day=?`, m.ID, old.Format("2006-01-02")).Scan(&total, &up); err != nil {
		t.Fatal(err)
	}
	if total != 2 || up != 1 {
		t.Fatalf("late result missing: total=%d, up=%d", total, up)
	}
	if err := s.readDB.QueryRow(`SELECT total FROM check_result_hourly WHERE monitor_id=? AND hour=?`, m.ID, now.Format("2006-01-02T15")).Scan(&open); err != nil || open != 1 {
		t.Fatalf("previously open hour was skipped: total=%d, error=%v", open, err)
	}
	var checkpoint string
	if err := s.readDB.QueryRow(`SELECT value FROM meta WHERE key='rollup_scan_id'`).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	insertRollupCheck(t, s, m.ID, old, "down", 300)
	if _, err := s.writeDB.Exec(`CREATE TRIGGER fail_daily BEFORE INSERT ON check_result_daily BEGIN SELECT RAISE(FAIL, 'injected daily failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RollupPending(ctx, now.Add(time.Hour)); err == nil {
		t.Fatal("injected daily failure ignored")
	}
	var after string
	if err := s.readDB.QueryRow(`SELECT value FROM meta WHERE key='rollup_scan_id'`).Scan(&after); err != nil || after != checkpoint {
		t.Fatalf("failed pass advanced checkpoint: before=%s, after=%s, error=%v", checkpoint, after, err)
	}
	if _, err := s.writeDB.Exec(`DROP TRIGGER fail_daily`); err != nil {
		t.Fatal(err)
	}
	if err := s.RollupPending(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.readDB.QueryRow(`SELECT total, up_count FROM check_result_daily WHERE monitor_id=? AND day=?`, m.ID, old.Format("2006-01-02")).Scan(&total, &up); err != nil {
		t.Fatal(err)
	}
	if total != 3 || up != 1 {
		t.Fatalf("retry missed failed day: total=%d, up=%d", total, up)
	}
}

func TestInterruptedPurgeProtectsCompleteSummary(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "interrupted purge")
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour).AddDate(0, 0, -10)
	times := make([]time.Time, purgeBatchSize+2)
	for i := range times {
		times[i] = hour
	}
	seedChecks(t, s, m.ID, times, func(int) string { return "up" })
	if _, err := s.writeDB.Exec(`CREATE TRIGGER interrupt_purge BEFORE DELETE ON check_results
		WHEN OLD.id > 5000 BEGIN SELECT RAISE(FAIL, 'injected deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeOldData(ctx, hour.Add(time.Hour)); err == nil {
		t.Fatal("expected second purge batch to fail")
	}
	var remaining int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM check_results`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("partial purge fixture: remaining=%d, error=%v", remaining, err)
	}
	// The surviving data plus late additions now outnumbers the original
	// summary, so a simple count comparison cannot detect partial source data.
	seedChecks(t, s, m.ID, times, func(int) string { return "down" })
	if err := s.RollupHourly(ctx, hour.Format("2006-01-02T15")); err != nil {
		t.Fatal(err)
	}
	var total, up int
	if err := s.readDB.QueryRow(`SELECT total, up_count FROM check_result_hourly WHERE monitor_id=? AND hour=?`, m.ID, hour.Format("2006-01-02T15")).Scan(&total, &up); err != nil {
		t.Fatal(err)
	}
	if total != len(times) || up != len(times) {
		t.Fatalf("partial sources replaced complete summary: total=%d, up=%d", total, up)
	}
}

func TestLegacyPartialSourcesKeepExistingSummaries(t *testing.T) {
	for _, tier := range []string{"raw", "hourly"} {
		t.Run(tier, func(t *testing.T) {
			s := testStore(t)
			m := newTestMonitor(t, s, "legacy partial")
			ctx := context.Background()
			day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
			for h := 0; h < 2; h++ {
				hour := day.Add(time.Duration(h) * time.Hour)
				insertRollupCheck(t, s, m.ID, hour.Add(time.Minute), "up", 100)
				insertRollupCheck(t, s, m.ID, hour.Add(40*time.Minute), "up", 100)
				if err := s.RollupHourly(ctx, hour.Format("2006-01-02T15")); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
			if tier == "raw" {
				if _, err := s.writeDB.Exec(`DELETE FROM check_results WHERE created_at < ?`, formatTime(day.Add(30*time.Minute))); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.writeDB.Exec(`DELETE FROM check_results WHERE created_at < ?`, formatTime(day.Add(time.Hour))); err != nil {
					t.Fatal(err)
				}
				if _, err := s.writeDB.Exec(`DELETE FROM check_result_hourly WHERE hour=?`, day.Format("2006-01-02T15")); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RollupPending(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}
			var total int
			if err := s.readDB.QueryRow(`SELECT total FROM check_result_daily WHERE monitor_id=? AND day=?`, m.ID, day.Format("2006-01-02")).Scan(&total); err != nil {
				t.Fatal(err)
			}
			if total != 4 {
				t.Errorf("legacy complete daily summary replaced: total=%d, want 4", total)
			}
		})
	}
}

func TestRetentionSummarizesOldBacklogAndHoles(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "backlog")
	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, days := range []int{120, 5, 2} {
		at := today.AddDate(0, 0, -days).Add(12 * time.Hour)
		insertRollupCheck(t, s, m.ID, at, "up", 100)
		if days == 2 {
			if err := s.RollupHourly(ctx, at.Format("2006-01-02T15")); err != nil {
				t.Fatal(err)
			}
			if err := s.RollupDaily(ctx, at.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
		}
	}
	NewRetentionWorker(s, 7, 7, time.Hour, discardLogger()).rollup(ctx)
	for _, days := range []int{120, 5, 2} {
		day := today.AddDate(0, 0, -days).Format("2006-01-02")
		var count int
		if err := s.readDB.QueryRow(`SELECT COALESCE(SUM(total),0) FROM check_result_daily WHERE monitor_id=? AND day=?`, m.ID, day).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s: summarized %d checks, want 1", day, count)
		}
	}
}

func TestPurgeRetainsRawOnSummaryFailure(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "summary failure")
	old := time.Now().UTC().AddDate(0, 0, -10)
	insertRollupCheck(t, s, m.ID, old, "down", 100)
	if _, err := s.writeDB.Exec(`CREATE TRIGGER fail_rollup BEFORE INSERT ON check_result_hourly BEGIN SELECT RAISE(FAIL, 'injected rollup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeOldData(context.Background(), old.Add(24*time.Hour)); err == nil {
		t.Error("purge succeeded despite a failed summary")
	}
	var count int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM check_results WHERE monitor_id=?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("raw checks after summary failure = %d, want 1", count)
	}
}

func TestRollupPreservesPurgedSourcesAfterRestart(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "purge restart")
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	for h := 0; h < 2; h++ {
		for i := 0; i < 3; i++ {
			insertRollupCheck(t, s, m.ID, day.Add(time.Duration(h)*time.Hour), "up", 100)
		}
		if err := s.RollupHourly(ctx, day.Add(time.Duration(h)*time.Hour).Format("2006-01-02T15")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeOldData(ctx, day.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	path := s.dbPath
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	insertRollupCheck(t, reopened, m.ID, day, "down", 500)
	if err := reopened.RollupHourly(ctx, day.Format("2006-01-02T15")); err != nil {
		t.Fatal(err)
	}
	var total, up int
	if err := reopened.readDB.QueryRow(`SELECT total, up_count FROM check_result_hourly WHERE monitor_id=? AND hour=?`, m.ID, day.Format("2006-01-02T15")).Scan(&total, &up); err != nil {
		t.Fatal(err)
	}
	if total != 3 || up != 3 {
		t.Errorf("purged hourly summary replaced: total=%d, up=%d; want 3, 3", total, up)
	}
	if _, err := reopened.PurgeHourlyBefore(ctx, day.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RollupHourly(ctx, day.Add(time.Hour).Format("2006-01-02T15")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.readDB.QueryRow(`SELECT total, up_count FROM check_result_daily WHERE monitor_id=? AND day=?`, m.ID, day.Format("2006-01-02")).Scan(&total, &up); err != nil {
		t.Fatal(err)
	}
	if total != 6 || up != 6 {
		t.Errorf("purged daily summary replaced: total=%d, up=%d; want 6, 6", total, up)
	}
}

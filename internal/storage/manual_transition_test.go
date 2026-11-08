package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func manualTransitionFixture(t *testing.T) (*SQLiteStore, *Monitor) {
	t.Helper()
	s := testStore(t)
	mon := &Monitor{Name: "manual", Type: "manual", Enabled: true, Interval: 60, Timeout: 10,
		FailureThreshold: 3, SuccessThreshold: 2}
	if err := s.CreateMonitor(context.Background(), mon); err != nil {
		t.Fatal(err)
	}
	return s, mon
}

func TestManualTransitionCommitsHistoryAndStatus(t *testing.T) {
	s, mon := manualTransitionFixture(t)
	ctx := context.Background()
	if _, err := s.writeDB.Exec(`UPDATE monitor_status SET last_body_hash='preserved', baseline_avg=42 WHERE monitor_id=?`, mon.ID); err != nil {
		t.Fatal(err)
	}
	for i, state := range []string{"up", "degraded", "down", "up"} {
		current, err := s.RecordManualStatus(ctx, mon.ID, state, "operator message", MonitorAccess{MonitorIDs: []int64{mon.ID}})
		if err != nil || current == nil || current.FailureThreshold != 3 || current.SuccessThreshold != 2 {
			t.Fatalf("transition config=%+v err=%v", current, err)
		}
		status, err := s.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		check, err := s.GetLatestCheckResult(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != state || check.Status != state || check.Message != "operator message" ||
			status.LastCheckAt == nil || !check.CreatedAt.Equal(*status.LastCheckAt) ||
			status.LastBodyHash != "preserved" || status.BaselineAvg != 42 {
			t.Fatalf("inconsistent history/status: check=%+v status=%+v", check, status)
		}
		wantFails, wantSuccesses := 3, 0
		if state == "up" {
			wantFails, wantSuccesses = 0, 2
		}
		if status.ConsecFails != wantFails || status.ConsecSuccesses != wantSuccesses {
			t.Fatalf("thresholds not applied: %+v", status)
		}
		checks, err := s.ListCheckResults(ctx, mon.ID, Pagination{Page: 1, PerPage: 10})
		if err != nil || checks.Total != int64(i+1) {
			t.Fatalf("history=%+v err=%v", checks, err)
		}
	}
}

func TestManualTransitionRollsBackOnEitherWriteFailure(t *testing.T) {
	for _, table := range []string{"check_results", "monitor_status"} {
		t.Run(table, func(t *testing.T) {
			s, mon := manualTransitionFixture(t)
			ctx := context.Background()
			if _, err := s.writeDB.Exec(`CREATE TRIGGER fail_manual BEFORE INSERT ON ` + table +
				` BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordManualStatus(ctx, mon.ID, "down", "failure"); err == nil {
				t.Fatal("failed write reported success")
			}
			assertManualUnchanged(t, s, mon.ID)
		})
	}
}

func TestManualTransitionRequiresCurrentTypeAndEveryScope(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	mon := monitors[0]
	mon.Type = "manual"
	if err := s.UpdateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	groupAccess := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	for _, filters := range [][]MonitorAccess{
		{{}},
		{{MonitorIDs: []int64{monitors[1].ID}}},
		{groupAccess, {MonitorIDs: []int64{monitors[1].ID}}},
	} {
		if _, err := s.RecordManualStatus(ctx, mon.ID, "down", "denied", filters...); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("denied scope %v returned %v", filters, err)
		}
		assertManualUnchanged(t, s, mon.ID)
	}
	if _, err := s.BulkSetMonitorGroup(ctx, []int64{mon.ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordManualStatus(ctx, mon.ID, "down", "stale group", groupAccess); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale group permitted: %v", err)
	}
	assertManualUnchanged(t, s, mon.ID)
	mon.Type = "http"
	if err := s.UpdateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordManualStatus(ctx, mon.ID, "down", "stale type", MonitorAccess{All: true}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("non-manual status write permitted: %v", err)
	}
	assertManualUnchanged(t, s, mon.ID)
}

func TestManualTransitionRejectsInvalidStateAndCancellation(t *testing.T) {
	s, mon := manualTransitionFixture(t)
	if _, err := s.RecordManualStatus(context.Background(), mon.ID, "pending", "invalid"); err == nil {
		t.Fatal("invalid state accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RecordManualStatus(ctx, mon.ID, "down", "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write returned %v", err)
	}
	assertManualUnchanged(t, s, mon.ID)
}

func assertManualUnchanged(t *testing.T, s *SQLiteStore, id int64) {
	t.Helper()
	ctx := context.Background()
	checks, err := s.ListCheckResults(ctx, id, Pagination{Page: 1, PerPage: 10})
	if err != nil || checks.Total != 0 {
		t.Fatalf("denied/failed transition left history: %+v %v", checks, err)
	}
	status, err := s.GetMonitorStatus(ctx, id)
	if err != nil || status.Status != "pending" || status.LastCheckAt != nil || status.ConsecFails != 0 || status.ConsecSuccesses != 0 {
		t.Fatalf("denied/failed transition changed status: %+v %v", status, err)
	}
}

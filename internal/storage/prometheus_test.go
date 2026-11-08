package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPrometheusSnapshotIncludesEveryMonitorAndOnlyScalarData(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var ids []int64
	for i := 0; i < 1027; i++ {
		// Deliberately unreadable settings/targets must never be loaded by metrics.
		id, err := insertID(ctx, tx, `INSERT INTO monitors (name,type,target,settings) VALUES (?, 'http', 'private-target', 'enc:unreadable')`, fmt.Sprintf("monitor-%04d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE monitors SET enabled=0 WHERE id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE monitors SET type='heartbeat' WHERE id=?`, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE monitors SET type='manual' WHERE id=?`, ids[3]); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 2, 3, 12, 0, 0, 0, time.UTC)
	for _, i := range []int{0, 1, 2, 3} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_status (monitor_id,status,last_check_at,last_response_time) VALUES (?, 'up', ?, 0)`, ids[i], formatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"region", "région"} {
		id, err := insertID(ctx, tx, `INSERT INTO tags (name) VALUES (?)`, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_tags (monitor_id,tag_id,value) VALUES (?,?,'before')`, ids[0], id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := []*RequestLog{{Method: "GET", Path: "/", ClientIP: "fixture", LatencyMs: 3, CreatedAt: now}}
	if err := s.InsertRequestLogBatch(ctx, before); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateIncident(ctx, &Incident{MonitorID: ids[0], Status: "open", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	seen := make(map[int64]int)
	var lastID int64
	var lastTagID int64
	summary, err := s.VisitPrometheus(ctx, now.Add(-time.Hour), now.Add(time.Hour), func(row PrometheusRow) error {
		if row.MonitorID < lastID {
			t.Fatalf("unordered monitor %d after %d", row.MonitorID, lastID)
		}
		if row.MonitorID != lastID {
			lastTagID = 0
		}
		lastID = row.MonitorID
		seen[row.MonitorID]++
		if len(seen) == 1 && seen[row.MonitorID] == 1 {
			// Commit changes between rows/batches and aggregate reads. Every value
			// below must still describe the snapshot established by the first read.
			if _, err := s.writeDB.Exec(`UPDATE monitors SET name='changed' WHERE id=?`, ids[len(ids)-1]); err != nil {
				return err
			}
			if _, err := s.writeDB.Exec(`UPDATE monitor_tags SET value='changed'`); err != nil {
				return err
			}
			if _, err := s.writeDB.Exec(`UPDATE incidents SET status='resolved'`); err != nil {
				return err
			}
			if err := s.InsertRequestLogBatch(ctx, before); err != nil {
				return err
			}
		}
		if row.MonitorID == ids[len(ids)-1] && row.Name != "monitor-1026" {
			t.Fatalf("later batch lost snapshot: %+v", row)
		}
		if row.Tag != nil {
			if row.Tag.TagID <= lastTagID || row.Tag.Value != "before" {
				t.Fatalf("tag snapshot/order: %+v", row.Tag)
			}
			lastTagID = row.Tag.TagID
		}
		switch row.MonitorID {
		case ids[0], ids[1]:
			if row.ResponseTime == nil || *row.ResponseTime != 0 {
				t.Fatal("measured zero response lost")
			}
		default:
			if row.ResponseTime != nil {
				t.Fatal("passive or unobserved response manufactured")
			}
		}
		if row.MonitorID == ids[1] && row.Status != "paused" {
			t.Fatal("paused monitor reported up")
		}
		if row.MonitorID == ids[4] && row.Status != "pending" {
			t.Fatal("missing status reported up")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(ids) || seen[ids[0]] != 2 {
		t.Fatalf("monitor/tag count: %d, first=%d", len(seen), seen[ids[0]])
	}
	if summary.OpenIncidents != 1 || summary.ResolvedIncidents != 0 || summary.Requests != 1 || summary.UniqueVisitors != 1 || summary.AvgLatencyMs != 3 {
		t.Fatalf("aggregate snapshot: %+v", summary)
	}
}

func TestPrometheusSnapshotPropagatesEveryFailure(t *testing.T) {
	for _, stage := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			s := testStore(t)
			newTestMonitor(t, s, "fixture")
			failure := errors.New("injected metrics read failure")
			calls := 0
			original := s.readDB.guard
			s.readDB.guard = func() error {
				calls++
				if calls == stage {
					return failure
				}
				return checkSQLGuard(original)
			}
			defer func() { s.readDB.guard = original }()
			got, err := s.VisitPrometheus(context.Background(), time.Now().Add(-time.Hour), time.Now(), func(PrometheusRow) error { return nil })
			// QueryRow cannot return a guard error directly; sqlPool deliberately
			// supplies a canceled context so Scan fails without executing SQL.
			want := failure
			if stage == 3 || stage == 4 {
				want = context.Canceled
			}
			if got != nil || !errors.Is(err, want) {
				t.Fatalf("stage %d returned partial success: %+v %v", stage, got, err)
			}
		})
	}
}

func TestPrometheusSnapshotStopsOnVisitorFailureAndCancellation(t *testing.T) {
	s := testStore(t)
	newTestMonitor(t, s, "fixture")
	ctx := context.Background()
	now := time.Now()
	failure := errors.New("output limit")
	if got, err := s.VisitPrometheus(ctx, now.Add(-time.Hour), now, func(PrometheusRow) error { return failure }); got != nil || !errors.Is(err, failure) {
		t.Fatalf("visitor failure: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if got, err := s.VisitPrometheus(ctx, now.Add(-time.Hour), now, func(PrometheusRow) error { cancel(); return nil }); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %+v %v", got, err)
	}
	if _, err := s.VisitPrometheus(context.Background(), now, now, nil); err == nil {
		t.Fatal("nil visitor accepted")
	}
}

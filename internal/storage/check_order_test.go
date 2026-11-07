package storage

import (
	"context"
	"testing"
	"time"
)

func TestCheckResultsWithSameTimestamp(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "same-second results")
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var ids []int64
	for _, rt := range []int64{100, 200} {
		res, err := s.writeDB.ExecContext(ctx,
			`INSERT INTO check_results (monitor_id, status, response_time, created_at) VALUES (?, 'up', ?, ?)`,
			m.ID, rt, formatTime(at))
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	latest, err := s.GetLatestCheckResult(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != ids[1] {
		t.Errorf("latest check = %d, want newest inserted ID %d", latest.ID, ids[1])
	}
	for page := 1; page <= 2; page++ {
		result, err := s.ListCheckResults(ctx, m.ID, Pagination{Page: page, PerPage: 1})
		if err != nil {
			t.Fatal(err)
		}
		rows := result.Data.([]*CheckResult)
		if len(rows) != 1 || rows[0].ID != ids[2-page] {
			t.Errorf("page %d = %+v, want ID %d", page, rows, ids[2-page])
		}
	}
	sparks, err := s.GetMonitorSparklines(ctx, []int64{m.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sparks[m.ID]; len(got) != 2 || got[0].ResponseTime != 100 || got[1].ResponseTime != 200 {
		t.Errorf("sparkline must retain insertion order: %+v", got)
	}
	points, err := s.GetResponseTimeSeries(ctx, m.ID, at, at.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].ResponseTime != 100 || points[1].ResponseTime != 200 {
		t.Errorf("chart must retain insertion order: %+v", points)
	}
}

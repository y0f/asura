package storage

import (
	"context"
	"testing"
	"time"
)

func TestSQLAggregateSemantics(t *testing.T) {
	testSQLAggregateSemantics(t, testStore(t))
}

func testSQLAggregateSemantics(t *testing.T, store *SQLStore) {
	t.Helper()
	ctx := context.Background()
	m := createTestMonitor(t, store, ctx, "aggregate contract")
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for i, response := range []int64{2147483648, 2147483649} {
		at := from.Add(time.Duration(i) * time.Second)
		if _, err := store.writeDB.ExecContext(ctx, `INSERT INTO check_results (monitor_id,status,response_time,created_at,observation_kind) VALUES (?,'up',?,?,'probe')`, m.ID, response, formatTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	points, err := store.GetResponseTimeSeries(ctx, m.ID, from, from.Add(time.Minute), 1)
	if err != nil || len(points) != 1 || points[0].ResponseTime != 2147483648 {
		t.Fatalf("64-bit average must truncate without overflow: %+v, %v", points, err)
	}
	for range 2 {
		if err := store.RollupHourly(ctx, "2026-01-02T00"); err != nil {
			t.Fatal(err)
		}
		if err := store.RollupDaily(ctx, "2026-01-02"); err != nil {
			t.Fatal(err)
		}
	}
	var count, average, sum, total int64
	if err := store.readDB.QueryRowContext(ctx, `SELECT COUNT(*), MIN(avg_rt), MIN(sum_rt), MIN(total) FROM check_result_daily WHERE monitor_id=? AND day=?`, m.ID, "2026-01-02").Scan(&count, &average, &sum, &total); err != nil || count != 1 || average != 2147483648 || sum != 4294967297 || total != 2 {
		t.Fatalf("rollup overwrite/numeric contract: count=%d average=%d sum=%d total=%d, error=%v", count, average, sum, total, err)
	}
	if err := store.InsertRequestLogBatch(ctx, []*RequestLog{
		{Method: "GET", Path: "/Case", LatencyMs: 1, ClientIP: "192.0.2.1", CreatedAt: from},
		{Method: "GET", Path: "/case", LatencyMs: 2, ClientIP: "192.0.2.2", CreatedAt: from},
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.GetRequestLogStats(ctx, from, from.Add(time.Minute))
	if err != nil || stats.TotalRequests != 2 || stats.AvgLatencyMs != 1 {
		t.Fatalf("request latency average must truncate: %+v, %v", stats, err)
	}
	logs, err := store.ListRequestLogs(ctx, RequestLogFilter{Path: "/CASE"}, Pagination{Page: 1, PerPage: 10})
	if err != nil || logs.Total != 2 {
		t.Fatalf("ASCII search parity: %+v, %v", logs, err)
	}
	if err := store.advancePurgeFloor(ctx, "contract_floor", "2026-02-01"); err != nil {
		t.Fatal(err)
	}
	if err := store.advancePurgeFloor(ctx, "contract_floor", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	var floor string
	if err := store.readDB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='contract_floor'`).Scan(&floor); err != nil || floor != "2026-02-01" {
		t.Fatalf("purge floor regressed: %q, %v", floor, err)
	}
}

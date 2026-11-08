package storage

import (
	"context"
	"testing"
	"time"
)

func TestObservationRollupsKeepKindsSeparate(t *testing.T) {
	testObservationRollupsKeepKindsSeparate(t, testStore(t))
}

func testObservationRollupsKeepKindsSeparate(t *testing.T, s *SQLStore) {
	t.Helper()
	ctx := context.Background()
	m := newTestMonitor(t, s, "mixed observations")
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	add := func(kind, status string, rt int64) {
		t.Helper()
		if _, err := s.writeDB.Exec(`INSERT INTO check_results
			(monitor_id,status,response_time,created_at,observation_kind) VALUES(?,?,?,?,?)`,
			m.ID, status, rt, formatTime(day.Add(time.Minute)), kind); err != nil {
			t.Fatal(err)
		}
	}
	rollup := func() {
		t.Helper()
		if err := s.RollupHourly(ctx, day.Format("2006-01-02T15")); err != nil {
			t.Fatal(err)
		}
		if err := s.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
	}
	assertUptime := func() {
		t.Helper()
		days, err := s.GetDailyUptime(ctx, m.ID, day, day.AddDate(0, 0, 1))
		pct, total := SummarizeUptime(days)
		if err != nil || total != 2 || pct != 50 {
			t.Fatalf("non-probes changed uptime: total=%d pct=%v err=%v", total, pct, err)
		}
	}
	add(ObservationUnknown, "down", 9000)
	rollup()
	add(ObservationManual, "up", 0)
	add(ObservationProbe, "up", 20)
	add(ObservationProbe, "down", 60)
	// Existing unknown summaries must not suppress newly arrived raw probes.
	assertUptime()
	rollup()
	for _, table := range []string{"check_result_hourly", "check_result_daily"} {
		var kinds, samples int
		if err := s.readDB.QueryRow("SELECT COUNT(*),SUM(total) FROM "+table+" WHERE monitor_id=?", m.ID).Scan(&kinds, &samples); err != nil || kinds != 3 || samples != 4 {
			t.Fatalf("%s merged observation kinds: kinds=%d total=%d err=%v", table, kinds, samples, err)
		}
	}
	assertUptime()
	points, err := s.GetTimeSeriesFromDaily(ctx, m.ID, day, day.AddDate(0, 0, 1))
	if err != nil || len(points) != 1 || points[0].ResponseTime != 40 {
		t.Fatalf("daily latency includes non-probes: %+v %v", points, err)
	}
	if _, err := s.PurgeOldData(ctx, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeHourlyBefore(ctx, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	assertUptime()
	// A late passive row below the purge floor cannot replace the complete
	// probe summary, and creating its own kind cannot destroy legacy history.
	add(ObservationManual, "down", 0)
	rollup()
	assertUptime()
	var legacyTotal int
	if err := s.readDB.QueryRow(`SELECT total FROM check_result_daily WHERE monitor_id=? AND observation_kind='unknown'`, m.ID).Scan(&legacyTotal); err != nil || legacyTotal != 1 {
		t.Fatalf("legacy summary was overwritten: %d %v", legacyTotal, err)
	}
}

func TestObservationTypeTransitionsNeverReclassifyHistory(t *testing.T) {
	testObservationTypeTransitionsNeverReclassifyHistory(t, testStore(t))
}

func testObservationTypeTransitionsNeverReclassifyHistory(t *testing.T, s *SQLStore) {
	t.Helper()
	ctx := context.Background()
	m := newTestMonitor(t, s, "mode changes")
	changeType := func(kind string) {
		t.Helper()
		m.Type = kind
		if err := s.UpdateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	assertLatency := func(want bool) {
		t.Helper()
		values, err := s.GetLatestResponseTimes(ctx)
		_, found := values[m.ID]
		if err != nil || found != want {
			t.Fatalf("latest latency availability=%v want=%v error=%v", found, want, err)
		}
	}
	for i := 0; i < 2; i++ {
		changeType("manual")
		if _, err := s.RecordManualStatus(ctx, m.ID, "down", "operator state"); err != nil {
			t.Fatal(err)
		}
		changeType("http")
		assertLatency(false)
		now := time.Now()
		result := &CheckResult{MonitorID: m.ID, Status: "up", ObservationKind: ObservationManual}
		status := &MonitorStatus{MonitorID: m.ID, Status: "up", LastCheckAt: &now, ObservationKind: ObservationHeartbeat}
		if err := s.RecordMonitorCheck(ctx, m, result, status); err != nil {
			t.Fatal(err)
		}
		if result.ObservationKind != ObservationProbe || status.ObservationKind != ObservationProbe {
			t.Fatal("caller-controlled observation kind escaped the trusted writer")
		}
		assertLatency(true) // A real measured 0 ms remains a sample.
	}
	checks, err := s.ListCheckResults(ctx, m.ID, Pagination{Page: 1, PerPage: 10})
	if err != nil || checks.Total != 4 {
		t.Fatalf("state history lost: %+v %v", checks, err)
	}
	kinds := map[string]int{}
	for _, check := range checks.Data.([]*CheckResult) {
		kinds[check.ObservationKind]++
	}
	if kinds[ObservationManual] != 2 || kinds[ObservationProbe] != 2 {
		t.Fatalf("history reclassified after type changes: %v", kinds)
	}
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	total, up, down, _, err := s.GetCheckCounts(ctx, m.ID, from, to)
	if err != nil || total != 2 || up != 2 || down != 0 {
		t.Fatalf("state transitions became check samples: %d/%d/%d %v", total, up, down, err)
	}
	// An out-of-order agent observation can use the history-only writer, but
	// its submitted kind still cannot classify the evidence or apply state.
	result := &CheckResult{MonitorID: m.ID, Status: "down", ObservationKind: ObservationHeartbeat}
	if err := s.RecordMonitorCheck(ctx, m, result, nil); err != nil || result.ObservationKind != ObservationProbe {
		t.Fatalf("history-only provenance was caller controlled: %+v %v", result, err)
	}
	status, err := s.GetMonitorStatus(ctx, m.ID)
	if err != nil || status.Status != "up" || status.ObservationKind != ObservationProbe {
		t.Fatalf("history-only result changed state: %+v %v", status, err)
	}
}

func TestObservationLatencySortIgnoresUnprovenValues(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()
	for _, tc := range []struct {
		name, monitorType, kind string
		latency                 int64
	}{
		{"a legacy", "http", ObservationUnknown, 9000},
		{"b manual", "manual", ObservationProbe, 8000},
		{"c switched", "http", ObservationManual, 7000},
		{"z measured zero", "http", ObservationProbe, 0},
	} {
		m := newTestMonitor(t, s, tc.name)
		m.Type = tc.monitorType
		if err := s.UpdateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertMonitorStatus(ctx, &MonitorStatus{MonitorID: m.ID, Status: "up", LastCheckAt: &now, ObservationKind: tc.kind, ResponseTime: tc.latency}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.ListMonitors(ctx, MonitorListFilter{Sort: "response_time"}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.Data.([]*Monitor)
	if len(rows) != 4 || rows[0].Name != "z measured zero" {
		t.Fatalf("non-probe values ranked as measured latency: %+v", rows)
	}
}

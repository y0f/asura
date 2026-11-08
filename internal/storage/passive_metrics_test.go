package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestPassiveMetricsDoNotInterpretTransitionsAsSamples(t *testing.T) {
	for _, kind := range []string{"manual", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			m := newTestMonitor(t, s, "state monitor")
			from, to := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Second)
			insertRollupCheck(t, s, m.ID, from.Add(time.Second), "up", 0)
			insertRollupCheck(t, s, m.ID, from.Add(time.Minute), "down", 8000)
			measured, err := s.GetMonitorMetricsSnapshot(ctx, m.ID, from, to)
			if err != nil || !measured.Sampled || measured.UptimePercent == nil || *measured.UptimePercent != 50 || measured.Total != 2 || measured.Response.Samples != 1 || measured.Response.P50 != 0 {
				t.Fatalf("active measurements lost valid zero: %+v %v", measured, err)
			}
			m.Type, m.SLATarget = kind, 99.9
			if err := s.UpdateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			data, err := s.GetMonitorMetricsSnapshot(ctx, m.ID, from, to)
			if err != nil || data.Sampled || data.UptimePercent != nil || data.Response != nil || data.Total != 0 {
				t.Fatalf("passive samples fabricated: %+v %v", data, err)
			}
			if _, err := s.GetMonitorMetricsSnapshot(ctx, m.ID, from, to, MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("passive scope bypass: %v", err)
			}
			days, err := s.GetDailyUptime(ctx, m.ID, from, to)
			if err != nil || len(days) != 0 {
				t.Fatalf("transitions counted as uptime: %v", err)
			}
			if _, err := s.GetUptimePercent(ctx, m.ID, from, to); !errors.Is(err, ErrNoMetricSamples) {
				t.Fatalf("passive percent must be unavailable: %v", err)
			}
			if data, err := s.GetMonitorSLASnapshot(ctx, m.ID, from, to, from); err != nil || data.Sampled || len(data.Period)+len(data.Rolling) != 0 {
				t.Fatalf("passive SLA data: %+v %v", data, err)
			}
			if sparks, err := s.GetMonitorSparklines(ctx, []int64{m.ID}, 20); err != nil || len(sparks) != 0 {
				t.Fatalf("passive sparkline: %v %v", sparks, err)
			}
			if times, err := s.GetLatestResponseTimes(ctx); err != nil || len(times) != 0 {
				t.Fatalf("passive latest latency: %v %v", times, err)
			}
			response, err := s.GetResponseTimeSummary(ctx, m.ID, from, to)
			if err != nil || response.Samples != 0 {
				t.Fatalf("passive latency fabricated: %v", err)
			}
			for _, window := range []time.Duration{time.Hour, 30 * 24 * time.Hour, 120 * 24 * time.Hour} {
				points, err := s.GetResponseTimeSeries(ctx, m.ID, to.Add(-window), to, 500)
				if err != nil || len(points) != 0 {
					t.Fatalf("passive response series: %d %v", len(points), err)
				}
			}
			ids, err := s.ListSLAMonitorIDs(ctx, 0, 100)
			if err != nil || len(ids) != 0 {
				t.Fatalf("inactive legacy SLA scanned: %v %v", ids, err)
			}
			if count, _, _, _, err := s.GetCheckCounts(ctx, m.ID, from, to); err != nil || count != 2 {
				t.Fatalf("state history was removed: %d %v", count, err)
			}
		})
	}
}

func TestMonitorMetricSnapshotsKeepAuthorityTypeAndDataTogether(t *testing.T) {
	for _, metric := range []string{"metrics", "sla"} {
		t.Run(metric, func(t *testing.T) {
			s, groups, monitors := accessFixture(t)
			ctx := context.Background()
			m := monitors[0]
			m.SLATarget = 99.9
			if err := s.UpdateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			from, to := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Second)
			insertRollupCheck(t, s, m.ID, from.Add(time.Minute), "up", 0)
			tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			reader := &badgeSnapshotReader{monitorReadQuery: tx, beforeMetric: func() {
				if _, err := s.writeDB.ExecContext(ctx, "UPDATE monitors SET type='manual', sla_target=50, group_id=? WHERE id=?", groups[1].ID, m.ID); err != nil {
					t.Fatal(err)
				}
				insertRollupCheck(t, s, m.ID, from.Add(2*time.Minute), "down", 9000)
			}}
			access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
			if metric == "metrics" {
				data, err := monitorMetricsSnapshotQuery(ctx, reader, m.ID, from, to, access)
				if err != nil || !data.Sampled || data.Total != 1 || data.UptimePercent == nil || *data.UptimePercent != 100 || data.Response.Samples != 1 {
					t.Fatalf("mixed metric snapshot: %+v %v", data, err)
				}
			} else {
				data, err := monitorSLASnapshotQuery(ctx, reader, m.ID, from, to, from, access)
				if err != nil {
					t.Fatal(err)
				}
				pct, total := SummarizeUptime(data.Period)
				rolling, rollingTotal := SummarizeUptime(data.Rolling)
				if !data.Sampled || data.Target != 99.9 || total != 1 || pct != 100 || rollingTotal != 1 || rolling != 100 {
					t.Fatalf("mixed SLA snapshot: %+v", data)
				}
			}
			if reader.beforeMetric != nil {
				t.Fatal("writer did not interleave")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetMonitorMetricsSnapshot(ctx, m.ID, from, to, access); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("new metrics snapshot ignored revoked authority: %v", err)
			}
			if _, err := s.GetMonitorSLASnapshot(ctx, m.ID, from, to, from, access); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("new SLA snapshot ignored revoked authority: %v", err)
			}
		})
	}
}

func TestMonitorMetricsSnapshotCountsRetainedRollups(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := newTestMonitor(t, s, "retained history")
	from := time.Now().UTC().Truncate(24 * time.Hour).Add(-72 * time.Hour)
	if _, err := s.writeDB.ExecContext(ctx, "INSERT INTO check_result_daily (monitor_id,day,total,up_count,down_count) VALUES (?,?,?,?,?)", m.ID, from.Format("2006-01-02"), 10, 7, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMonitorMetricsSnapshot(ctx, m.ID, from, from.Add(24*time.Hour))
	if err != nil || got.Total != 10 || got.Up != 7 || got.Down != 2 || got.Degraded != 1 || got.UptimePercent == nil || *got.UptimePercent != 70 || got.Response.Samples != 0 {
		t.Fatalf("retained count/rate mismatch: %+v %v", got, err)
	}
	if _, err := s.GetMonitorMetricsSnapshot(ctx, m.ID, from, from.Add(time.Hour), MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("metrics scope: %v", err)
	}
}

func TestLatestResponseTimesDistinguishesPendingFromMeasuredZero(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := newTestMonitor(t, s, "pending")
	got, err := s.GetLatestResponseTimes(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("pending monitor has latency: %v %v", got, err)
	}
	now := time.Now().UTC()
	if err := s.UpsertMonitorStatus(ctx, &MonitorStatus{MonitorID: m.ID, Status: "up", LastCheckAt: &now, ResponseTime: 0}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetLatestResponseTimes(ctx)
	if value, present := got[m.ID]; err != nil || !present || value != 0 {
		t.Fatalf("measured zero lost: %v %v", got, err)
	}
}

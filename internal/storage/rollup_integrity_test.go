package storage

import (
	"context"
	"math"
	"testing"
	"time"
)

func insertRollupCheck(t *testing.T, s *SQLiteStore, id int64, at time.Time, status string, rt int64) {
	t.Helper()
	if _, err := s.writeDB.Exec(`INSERT INTO check_results (monitor_id, status, response_time, created_at, observation_kind) VALUES (?, ?, ?, ?, 'probe')`, id, status, rt, formatTime(at)); err != nil {
		t.Fatal(err)
	}
}

func TestDailyRollupWeightsSamples(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hours [][]int64
		want  int64
	}{
		{"unequal counts", [][]int64{{100, 100, 100, 100, 100, 100, 100, 100, 100}, {1000}}, 190},
		{"fractional hourly mean", [][]int64{{100, 101}, {102}}, 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			m := newTestMonitor(t, s, tc.name)
			ctx := context.Background()
			day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			for h, samples := range tc.hours {
				hour := day.Add(time.Duration(h) * time.Hour)
				for _, rt := range samples {
					insertRollupCheck(t, s, m.ID, hour, "up", rt)
				}
				if err := s.RollupHourly(ctx, hour.Format("2006-01-02T15")); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
			points, err := s.GetTimeSeriesFromDaily(ctx, m.ID, day, day.AddDate(0, 0, 1))
			if err != nil {
				t.Fatal(err)
			}
			if len(points) != 1 {
				t.Fatalf("daily point count = %d, want 1", len(points))
			}
			if points[0].ResponseTime != tc.want {
				t.Errorf("daily mean = %d ms, want %d ms", points[0].ResponseTime, tc.want)
			}
		})
	}
}

func TestRollupPreservesDegraded(t *testing.T) {
	for _, tc := range []struct {
		statuses []string
		want     string
	}{
		{[]string{"up"}, "up"},
		{[]string{"degraded"}, "degraded"},
		{[]string{"up", "degraded"}, "degraded"},
		{[]string{"up", "degraded", "down"}, "down"},
	} {
		t.Run(tc.want+"/"+tc.statuses[0], func(t *testing.T) {
			s := testStore(t)
			m := newTestMonitor(t, s, "status rollup")
			ctx := context.Background()
			day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			for _, status := range tc.statuses {
				insertRollupCheck(t, s, m.ID, day, status, 100)
			}
			if err := s.RollupHourly(ctx, day.Format("2006-01-02T15")); err != nil {
				t.Fatal(err)
			}
			if err := s.RollupDaily(ctx, day.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
			hourly, err := s.GetTimeSeriesFromHourly(ctx, m.ID, day, day.Add(time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			daily, err := s.GetTimeSeriesFromDaily(ctx, m.ID, day, day.AddDate(0, 0, 1))
			if err != nil {
				t.Fatal(err)
			}
			for name, points := range map[string][]*TimeSeriesPoint{"hourly": hourly, "daily": daily} {
				if len(points) != 1 {
					t.Fatalf("%s point count = %d, want 1", name, len(points))
				}
				if points[0].Status != tc.want {
					t.Errorf("%s status = %s, want %s", name, points[0].Status, tc.want)
				}
			}
		})
	}
}

func TestUptimeRollupGapsAndPartialDays(t *testing.T) {
	for _, tc := range []struct {
		name           string
		rolled         []int
		partial, purge bool
	}{
		{name: "missing middle", rolled: []int{0, 2}},
		{name: "missing oldest", rolled: []int{1, 2}},
		{name: "partial edges", rolled: []int{0, 2}, partial: true},
		{name: "purged covered days", rolled: []int{0, 2}, purge: true},
		{name: "all covered", rolled: []int{0, 1, 2}, purge: true},
		{name: "raw only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			m := newTestMonitor(t, s, "uptime gaps")
			ctx := context.Background()
			day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			from, to := day, day.AddDate(0, 0, 3)
			for d, status := range []string{"up", "down", "up"} {
				at := day.AddDate(0, 0, d).Add(12 * time.Hour)
				if tc.partial && d == 0 {
					at = at.Add(3 * time.Hour)
				}
				if tc.partial && d == 2 {
					at = at.Add(-3 * time.Hour)
				}
				insertRollupCheck(t, s, m.ID, at, status, 100)
			}
			if tc.partial {
				insertRollupCheck(t, s, m.ID, day.Add(9*time.Hour), "down", 100)
				insertRollupCheck(t, s, m.ID, day.AddDate(0, 0, 2).Add(15*time.Hour), "down", 100)
				from, to = day.Add(12*time.Hour), day.AddDate(0, 0, 2).Add(12*time.Hour)
			}
			for _, d := range tc.rolled {
				start := day.AddDate(0, 0, d)
				for h := 0; h < 24; h++ {
					if err := s.RollupHourly(ctx, start.Add(time.Duration(h)*time.Hour).Format("2006-01-02T15")); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.RollupDaily(ctx, start.Format("2006-01-02")); err != nil {
					t.Fatal(err)
				}
				if tc.purge {
					if _, err := s.writeDB.Exec(`DELETE FROM check_results WHERE monitor_id=? AND created_at>=? AND created_at<?`, m.ID, formatTime(start), formatTime(start.AddDate(0, 0, 1))); err != nil {
						t.Fatal(err)
					}
				}
			}
			pct, err := s.GetUptimePercent(ctx, m.ID, from, to)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(pct-200.0/3) > 0.001 {
				t.Errorf("uptime = %f, want 66.6667", pct)
			}
			days, err := s.GetDailyUptime(ctx, m.ID, from, to)
			if err != nil {
				t.Fatal(err)
			}
			if len(days) != 3 {
				t.Fatalf("daily uptime has %d days, want 3", len(days))
			}
			for i, d := range days {
				want := 100.0
				if i == 1 {
					want = 0
				}
				if d.TotalChecks != 1 || d.UptimePct != want {
					t.Errorf("day %s = %+v, want one sample and %g%%", d.Date, d, want)
				}
			}
		})
	}
}

package storage

import "testing"

func TestSummarizeUptimePreservesSampleEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		days    []*DailyUptime
		percent float64
		total   int64
	}{
		{name: "empty"},
		{name: "empty days", days: []*DailyUptime{nil, {UptimePct: 100}}},
		{name: "all failed", days: []*DailyUptime{{TotalChecks: 10, UpChecks: 0}}, total: 10},
		{name: "all up", days: []*DailyUptime{{TotalChecks: 10, UpChecks: 10}}, percent: 100, total: 10},
		{name: "unequal daily counts", days: []*DailyUptime{{TotalChecks: 90, UpChecks: 90, UptimePct: 100}, {TotalChecks: 10, UpChecks: 0, UptimePct: 0}}, percent: 90, total: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			percent, total := SummarizeUptime(tc.days)
			if percent != tc.percent || total != tc.total {
				t.Fatalf("summary = (%v, %d), want (%v, %d)", percent, total, tc.percent, tc.total)
			}
		})
	}
}

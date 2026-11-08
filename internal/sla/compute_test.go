package sla

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type fixedUptimeStore struct {
	storage.Store
	pct float64
}

func (f fixedUptimeStore) GetUptimePercent(context.Context, int64, time.Time, time.Time) (float64, error) {
	return f.pct, nil
}

func (f fixedUptimeStore) GetDailyUptime(context.Context, int64, time.Time, time.Time) ([]*storage.DailyUptime, error) {
	// Preserve two decimal places in the fixture's percentage using exact counts.
	up := int64(math.Round(f.pct * 100))
	return []*storage.DailyUptime{{TotalChecks: 10000, UpChecks: up, DownChecks: 10000 - up}}, nil
}

func TestComputeBreachedUsesMonthlyBudget(t *testing.T) {
	early := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) // 10 minutes into November
	late := time.Date(2026, 11, 30, 23, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		now    time.Time
		target float64
		uptime float64
		want   bool
	}{
		// 30% down for 10 minutes is 180s of a ~2592s budget.
		{"short outage early in month", early, 99.9, 70, false},
		{"just over target early in month", early, 99, 98.95, false},
		{"over target at month end", late, 99, 98.0, true},
		{"within target at month end", late, 99, 99.5, false},
		{"100% target any downtime", early, 100, 99.99, true},
		{"no target", late, 0, 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := computeAt(context.Background(), fixedUptimeStore{pct: tt.uptime}, 1, tt.target, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			if st.Breached != tt.want {
				t.Fatalf("Breached = %v, want %v (budget used %ds of %ds, %.1f%% left)",
					st.Breached, tt.want, st.BudgetUsedSecs, st.BudgetTotalSecs, st.BudgetRemainPct)
			}
			if st.Breached && st.BudgetRemainSecs > 0 {
				t.Fatalf("breached while %ds of budget remain", st.BudgetRemainSecs)
			}
		})
	}
}

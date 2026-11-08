package sla

import (
	"math"
	"testing"
)

func TestMonthlyBudgetExactDecimalAndSampleBoundaries(t *testing.T) {
	month := int64(30 * 86400)
	for _, tc := range []struct {
		name                 string
		target               float64
		total, up, elapsed   int64
		budget, used, remain int64
		percent              float64
		breached             bool
	}{
		{"exact 99.9 allowance", 99.9, 1000, 999, month, 2592, 2592, 0, 0, false},
		{"next failed sample", 99.9, 1000, 998, month, 2592, 5184, 0, 0, true},
		{"fractional allowance remains", 99.99, 10000, 9999, month - 1, 259, 259, 0, 100.0 / float64(month), false},
		{"no samples", 100, 0, 0, month, 0, 0, 0, 100, false},
		{"perfect target no loss", 100, 1000, 1000, month, 0, 0, 0, 100, false},
		{"perfect target fractional loss", 100, 1000, 999, 1, 0, 0, 0, 0, true},
		{"exact ten percent remaining", 99, 1000, 991, month, 25920, 23328, 2592, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := monthlyBudget(tc.target, tc.total, tc.up, month, tc.elapsed)
			if got.Total != tc.budget || got.Used != tc.used || got.Remain != tc.remain || math.Abs(got.RemainingPercent-tc.percent) > 1e-10 || got.Breached != tc.breached {
				t.Fatalf("budget=%+v, want total/used/remain=%d/%d/%d percent=%v breached=%v", got, tc.budget, tc.used, tc.remain, tc.percent, tc.breached)
			}
		})
	}
}

func TestBudgetInvalidAndFractionalInputs(t *testing.T) {
	for _, target := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -1, 100} {
		if got := ErrorBudgetSecs(target, 30*86400); got != 0 {
			t.Errorf("invalid/zero allowance %v=%d", target, got)
		}
	}
	if got := ErrorBudgetSecs(99.999, 30*86400); got != 25 {
		t.Errorf("fractional allowance rounded upward: %d", got)
	}
	if got := ErrorBudgetSecs(99.9, -1); got != 0 {
		t.Errorf("negative period accepted: %d", got)
	}
	if got := DowntimeSecs(99.9, 30*86400); got != 2592 {
		t.Errorf("decimal downtime lost a second: %d", got)
	}
}

func TestMonthlyBudgetRiskUsesExactFractionBeforeDisplayRounding(t *testing.T) {
	// These adjacent sample counts all display 10% remaining, but only the
	// exact boundary and the value below it have reached the warning threshold.
	const total, boundary = int64(1e18), int64(109e15)
	for _, offset := range []int64{-1, 0, 1} {
		got := monthlyBudget(1, total, boundary+offset, 30*86400, 30*86400)
		if got.RemainingPercent != 10 || got.AtRisk != (offset <= 0) {
			t.Errorf("boundary offset %d: %+v", offset, got)
		}
	}
}

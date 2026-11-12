package views

import (
	"testing"

	"github.com/y0f/asura/v3/internal/sla"
)

func TestSLAStatesUseSamplesAndExactRiskDecision(t *testing.T) {
	for _, tc := range []struct {
		name      string
		checks    int64
		remaining float64
		atRisk    bool
		breached  bool
		want      string
	}{
		{"no samples", 0, 100, false, false, "No checks"},
		{"100 percent target without downtime", 5, 100, false, false, "Healthy"},
		{"fractional allowance healthy", 5, 50, false, false, "Healthy"},
		{"fractional allowance at risk", 5, 5, true, false, "At risk"},
		{"exact warning boundary", 5, 10, true, false, "At risk"},
		{"display rounded to warning boundary", 5, 10, false, false, "Healthy"},
		{"breached", 5, 0, false, true, "Breached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Whole-second budget fields intentionally remain zero: presentation
			// rounding must not choose the state of a fractional allowance.
			entry := &sla.ReportEntry{TotalChecks: tc.checks, BudgetRemainPct: tc.remaining, BudgetAtRisk: tc.atRisk, Breached: tc.breached}
			if label, _ := slaState(entry); label != tc.want {
				t.Errorf("report state=%q, want %q", label, tc.want)
			}
			p := MonitorDetailParams{SLA: &SLAData{TotalChecks: tc.checks, BudgetRemainPct: tc.remaining, BudgetAtRisk: tc.atRisk, Breached: tc.breached}}
			if label, _ := p.slaState(); label != tc.want {
				t.Errorf("detail state=%q, want %q", label, tc.want)
			}
		})
	}
	p := SLAReportParams{Entries: []*sla.ReportEntry{{TotalChecks: 0}, {TotalChecks: 10}, {TotalChecks: 10, Breached: true}}}
	if p.meetingSLA() != 1 {
		t.Fatal("unmeasured or breached entries counted as meeting the target")
	}
}

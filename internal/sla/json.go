package sla

import "encoding/json"

// Observations must remain distinguishable from an empty history. The target
// and theoretical allowance exist before any checks; consumed/remaining budget
// and compliance do not.
func observed[T any](value T, samples int64) *T {
	if samples == 0 {
		return nil
	}
	return &value
}

func (s Status) MarshalJSON() ([]byte, error) {
	type status Status
	return json.Marshal(struct {
		status
		HasData     bool     `json:"has_data"`
		UptimeMonth *float64 `json:"uptime_pct_month"`
		Uptime30d   *float64 `json:"uptime_pct_30d"`
		Used        *int64   `json:"budget_used_secs"`
		Remain      *int64   `json:"budget_remain_secs"`
		RemainPct   *float64 `json:"budget_remain_pct"`
		AtRisk      *bool    `json:"budget_at_risk"`
		RemainHuman *string  `json:"budget_remain_human"`
		Breached    *bool    `json:"breached"`
	}{status(s), s.TotalChecks > 0, observed(s.UptimePctMonth, s.TotalChecks), observed(s.UptimePct30d, s.TotalChecks30d), observed(s.BudgetUsedSecs, s.TotalChecks), observed(s.BudgetRemainSecs, s.TotalChecks), observed(s.BudgetRemainPct, s.TotalChecks), observed(s.BudgetAtRisk, s.TotalChecks), observed(s.BudgetRemainHuman, s.TotalChecks), observed(s.Breached, s.TotalChecks)})
}

func (s ReportEntry) MarshalJSON() ([]byte, error) {
	type report ReportEntry
	return json.Marshal(struct {
		report
		HasData   bool     `json:"has_data"`
		Uptime    *float64 `json:"uptime_pct"`
		Used      *int64   `json:"used_secs"`
		Remain    *int64   `json:"remain_secs"`
		RemainPct *float64 `json:"budget_remain_pct"`
		AtRisk    *bool    `json:"budget_at_risk"`
		Breached  *bool    `json:"breached"`
	}{report(s), s.TotalChecks > 0, observed(s.UptimePct, s.TotalChecks), observed(s.UsedSecs, s.TotalChecks), observed(s.RemainSecs, s.TotalChecks), observed(s.BudgetRemainPct, s.TotalChecks), observed(s.BudgetAtRisk, s.TotalChecks), observed(s.Breached, s.TotalChecks)})
}

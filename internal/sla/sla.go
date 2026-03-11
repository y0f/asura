package sla

import (
	"context"
	"fmt"
	"time"

	"github.com/y0f/asura/internal/storage"
)

// Status represents the current SLA compliance state.
type Status struct {
	Target            float64 `json:"target"`
	UptimePctMonth    float64 `json:"uptime_pct_month"`
	UptimePct30d      float64 `json:"uptime_pct_30d"`
	MonthStart        string  `json:"month_start"`
	MonthEnd          string  `json:"month_end"`
	TotalSecsMonth    int64   `json:"total_secs_month"`
	BudgetTotalSecs   int64   `json:"budget_total_secs"`
	BudgetUsedSecs    int64   `json:"budget_used_secs"`
	BudgetRemainSecs  int64   `json:"budget_remain_secs"`
	BudgetRemainPct   float64 `json:"budget_remain_pct"`
	BudgetRemainHuman string  `json:"budget_remain_human"`
	Breached          bool    `json:"breached"`
}

// ReportEntry holds SLA data for one monitor in a monthly report.
type ReportEntry struct {
	MonitorID   int64   `json:"monitor_id"`
	MonitorName string  `json:"monitor_name"`
	Target      float64 `json:"target"`
	UptimePct   float64 `json:"uptime_pct"`
	TotalChecks int64   `json:"total_checks"`
	UpChecks    int64   `json:"up_checks"`
	DownChecks  int64   `json:"down_checks"`
	BudgetSecs  int64   `json:"budget_secs"`
	UsedSecs    int64   `json:"used_secs"`
	RemainSecs  int64   `json:"remain_secs"`
	Breached    bool    `json:"breached"`
}

// Compute calculates the SLA status for a monitor.
func Compute(ctx context.Context, store storage.Store, monitorID int64, target float64) (*Status, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	uptimeMonth, err := store.GetUptimePercent(ctx, monitorID, monthStart, now)
	if err != nil {
		return nil, fmt.Errorf("get monthly uptime: %w", err)
	}

	uptime30d, err := store.GetUptimePercent(ctx, monitorID, now.Add(-30*24*time.Hour), now)
	if err != nil {
		return nil, fmt.Errorf("get 30d uptime: %w", err)
	}

	totalSecsMonth := int64(monthEnd.Sub(monthStart).Seconds())
	elapsedSecs := int64(now.Sub(monthStart).Seconds())
	budgetTotal := ErrorBudgetSecs(target, totalSecsMonth)
	budgetUsed := DowntimeSecs(uptimeMonth, elapsedSecs)
	budgetRemain := budgetTotal - budgetUsed
	if budgetRemain < 0 {
		budgetRemain = 0
	}

	var budgetRemainPct float64
	if budgetTotal > 0 {
		budgetRemainPct = float64(budgetRemain) / float64(budgetTotal) * 100
	}

	return &Status{
		Target:            target,
		UptimePctMonth:    uptimeMonth,
		UptimePct30d:      uptime30d,
		MonthStart:        monthStart.Format("2006-01-02"),
		MonthEnd:          monthEnd.Format("2006-01-02"),
		TotalSecsMonth:    totalSecsMonth,
		BudgetTotalSecs:   budgetTotal,
		BudgetUsedSecs:    budgetUsed,
		BudgetRemainSecs:  budgetRemain,
		BudgetRemainPct:   budgetRemainPct,
		BudgetRemainHuman: FormatDuration(budgetRemain),
		Breached:          uptimeMonth < target,
	}, nil
}

// ComputeReport generates an SLA report for all monitors with SLA targets for a given month.
func ComputeReport(ctx context.Context, store storage.Store, year int, month time.Month) ([]*ReportEntry, error) {
	monitors, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10000})
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	monList, ok := monitors.Data.([]*storage.Monitor)
	if !ok {
		return nil, nil
	}

	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	now := time.Now().UTC()
	if monthEnd.After(now) {
		monthEnd = now
	}
	totalSecs := int64(monthEnd.Sub(monthStart).Seconds())

	var entries []*ReportEntry
	for _, m := range monList {
		if m.SLATarget <= 0 {
			continue
		}

		uptimePct, err := store.GetUptimePercent(ctx, m.ID, monthStart, monthEnd)
		if err != nil {
			continue
		}

		total, up, down, _, err := store.GetCheckCounts(ctx, m.ID, monthStart, monthEnd)
		if err != nil {
			continue
		}

		budgetSecs := ErrorBudgetSecs(m.SLATarget, totalSecs)
		elapsedSecs := totalSecs
		usedSecs := DowntimeSecs(uptimePct, elapsedSecs)
		remainSecs := budgetSecs - usedSecs
		if remainSecs < 0 {
			remainSecs = 0
		}

		entries = append(entries, &ReportEntry{
			MonitorID:   m.ID,
			MonitorName: m.Name,
			Target:      m.SLATarget,
			UptimePct:   uptimePct,
			TotalChecks: total,
			UpChecks:    up,
			DownChecks:  down,
			BudgetSecs:  budgetSecs,
			UsedSecs:    usedSecs,
			RemainSecs:  remainSecs,
			Breached:    uptimePct < m.SLATarget,
		})
	}
	return entries, nil
}

// ErrorBudgetSecs returns the total allowed downtime seconds for a given SLA target.
func ErrorBudgetSecs(target float64, totalSecs int64) int64 {
	if target <= 0 || target >= 100 {
		return 0
	}
	return int64(float64(totalSecs) * (100 - target) / 100)
}

// DowntimeSecs returns the estimated downtime seconds based on uptime percentage and elapsed time.
func DowntimeSecs(uptimePct float64, elapsedSecs int64) int64 {
	if uptimePct >= 100 || elapsedSecs <= 0 {
		return 0
	}
	return int64(float64(elapsedSecs) * (100 - uptimePct) / 100)
}

// FormatDuration formats seconds into a human-readable string like "4m 23s" or "2h 15m".
func FormatDuration(secs int64) string {
	if secs <= 0 {
		return "0s"
	}
	days := secs / 86400
	secs %= 86400
	hours := secs / 3600
	secs %= 3600
	minutes := secs / 60
	seconds := secs % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// BudgetStatus returns a status string based on remaining budget percentage.
func BudgetStatus(remainPct float64) string {
	if remainPct <= 0 {
		return "breached"
	}
	if remainPct <= 10 {
		return "critical"
	}
	if remainPct <= 25 {
		return "warning"
	}
	return "healthy"
}

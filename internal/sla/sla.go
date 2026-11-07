package sla

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/y0f/asura/internal/storage"
)

var ErrFuturePeriod = errors.New("SLA reports are only available for the current or a past month")

// ParsePeriod validates the common web/API report selector.
func ParsePeriod(value string, now time.Time) (int, time.Month, error) {
	period := now.UTC()
	if value != "" {
		var err error
		period, err = time.Parse("2006-01", value)
		if err != nil || period.Year() < 1 || period.Year() > 9999 {
			return 0, 0, fmt.Errorf("invalid period format, use YYYY-MM")
		}
	}
	start := time.Date(period.Year(), period.Month(), 1, 0, 0, 0, 0, time.UTC)
	if start.After(now.UTC()) {
		return 0, 0, ErrFuturePeriod
	}
	return period.Year(), period.Month(), nil
}

func monthlyBudget(target, uptime float64, totalSecs, elapsedSecs int64) (total, used, remain int64, breached bool) {
	total = ErrorBudgetSecs(target, totalSecs)
	used = DowntimeSecs(uptime, elapsedSecs)
	remain = max(total-used, 0)
	// Compare before whole-second display rounding. The allowance always
	// covers the full month, while usage covers only elapsed time.
	breached = target > 0 && float64(elapsedSecs)*(100-uptime) > float64(totalSecs)*(100-min(target, 100))
	return
}

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
	return computeAt(ctx, store, monitorID, target, time.Now().UTC())
}

func computeAt(ctx context.Context, store storage.Store, monitorID int64, target float64, now time.Time) (*Status, error) {
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
	budgetTotal, budgetUsed, budgetRemain, breached := monthlyBudget(target, uptimeMonth, totalSecsMonth, elapsedSecs)

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
		Breached:          breached,
	}, nil
}

// ComputeReport generates an SLA report for all monitors with SLA targets for a given month.
func ComputeReport(ctx context.Context, store storage.Store, year int, month time.Month) ([]*ReportEntry, error) {
	return computeReportAt(ctx, store, year, month, time.Now().UTC())
}

func computeReportAt(ctx context.Context, store storage.Store, year int, month time.Month, now time.Time) ([]*ReportEntry, error) {
	if year < 1 || year > 9999 || month < time.January || month > time.December {
		return nil, fmt.Errorf("invalid SLA report month")
	}
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	if monthStart.After(now) {
		return nil, ErrFuturePeriod
	}
	totalSecs := int64(monthEnd.Sub(monthStart).Seconds())
	until := monthEnd
	if until.After(now) {
		until = now
	}
	elapsedSecs := int64(until.Sub(monthStart).Seconds())

	var entries []*ReportEntry
	for page := 1; ; page++ {
		monitors, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: page, PerPage: 500})
		if err != nil {
			return nil, fmt.Errorf("list monitors: %w", err)
		}
		monList, ok := monitors.Data.([]*storage.Monitor)
		if !ok {
			return nil, fmt.Errorf("unexpected monitor list type %T", monitors.Data)
		}
		for _, m := range monList {
			if m.SLATarget <= 0 {
				continue
			}

			days, err := store.GetDailyUptime(ctx, m.ID, monthStart, until)
			if err != nil {
				return nil, fmt.Errorf("get uptime for monitor %d: %w", m.ID, err)
			}
			var total, up, down int64
			for _, day := range days {
				total += day.TotalChecks
				up += day.UpChecks
				down += day.DownChecks
			}
			uptimePct := 100.0
			if total > 0 {
				uptimePct = float64(up) / float64(total) * 100
			}
			budgetSecs, usedSecs, remainSecs, breached := monthlyBudget(m.SLATarget, uptimePct, totalSecs, elapsedSecs)

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
				Breached:    breached,
			})
		}
		if page >= monitors.TotalPages {
			break
		}
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

package sla

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

var ErrFuturePeriod = errors.New("SLA reports are only available for the current or a past month")
var ErrUnavailable = errors.New("sample-based SLA is unavailable for manual and heartbeat monitors")

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

// Status represents the current SLA compliance state.
type Status struct {
	Target            float64 `json:"target"`
	TotalChecks       int64   `json:"total_checks"`
	TotalChecks30d    int64   `json:"total_checks_30d"`
	UptimePctMonth    float64 `json:"uptime_pct_month"`
	UptimePct30d      float64 `json:"uptime_pct_30d"`
	MonthStart        string  `json:"month_start"`
	MonthEnd          string  `json:"month_end"`
	TotalSecsMonth    int64   `json:"total_secs_month"`
	BudgetTotalSecs   int64   `json:"budget_total_secs"`
	BudgetUsedSecs    int64   `json:"budget_used_secs"`
	BudgetRemainSecs  int64   `json:"budget_remain_secs"`
	BudgetRemainPct   float64 `json:"budget_remain_pct"`
	BudgetAtRisk      bool    `json:"budget_at_risk"`
	BudgetRemainHuman string  `json:"budget_remain_human"`
	Breached          bool    `json:"breached"`
}

// ReportEntry holds SLA data for one monitor in a monthly report.
type ReportEntry struct {
	MonitorGroupID  *int64  `json:"-"`
	MonitorID       int64   `json:"monitor_id"`
	MonitorName     string  `json:"monitor_name"`
	Target          float64 `json:"target"`
	UptimePct       float64 `json:"uptime_pct"`
	TotalChecks     int64   `json:"total_checks"`
	UpChecks        int64   `json:"up_checks"`
	DownChecks      int64   `json:"down_checks"`
	BudgetSecs      int64   `json:"budget_secs"`
	UsedSecs        int64   `json:"used_secs"`
	RemainSecs      int64   `json:"remain_secs"`
	BudgetRemainPct float64 `json:"budget_remain_pct"`
	BudgetAtRisk    bool    `json:"budget_at_risk"`
	Breached        bool    `json:"breached"`
}

// Compute calculates the SLA status for a monitor.
func Compute(ctx context.Context, store storage.Store, monitorID int64, access ...storage.MonitorAccess) (*Status, error) {
	return computeAt(ctx, store, monitorID, time.Now().UTC(), access...)
}

func computeAt(ctx context.Context, store storage.Store, monitorID int64, now time.Time, access ...storage.MonitorAccess) (*Status, error) {
	return computeSnapshot(ctx, store, monitorID, now, true, access...)
}

// ComputeMonthly reads one rollup-aware snapshot for the current month's SLA.
// Alert evaluation does not need the additional rolling-30-day chart query.
func ComputeMonthly(ctx context.Context, store storage.Store, monitorID int64, now time.Time, access ...storage.MonitorAccess) (*Status, error) {
	return computeSnapshot(ctx, store, monitorID, now, false, access...)
}

func computeSnapshot(ctx context.Context, store storage.Store, monitorID int64, now time.Time, includeRolling bool, access ...storage.MonitorAccess) (*Status, error) {
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	var rollingFrom time.Time
	if includeRolling {
		rollingFrom = now.Add(-30 * 24 * time.Hour)
	}
	data, err := store.GetMonitorSLASnapshot(ctx, monitorID, monthStart, now, rollingFrom, access...)
	if err != nil {
		return nil, fmt.Errorf("get monthly uptime: %w", err)
	}
	if !data.Sampled {
		return nil, ErrUnavailable
	}
	var totalChecks, upChecks int64
	for _, day := range data.Period {
		totalChecks += day.TotalChecks
		upChecks += day.UpChecks
	}
	uptimeMonth := 0.0
	if totalChecks > 0 {
		uptimeMonth = float64(upChecks) / float64(totalChecks) * 100
	}

	totalSecsMonth := int64(monthEnd.Sub(monthStart).Seconds())
	elapsedSecs := int64(now.Sub(monthStart).Seconds())
	budget := monthlyBudget(data.Target, totalChecks, upChecks, totalSecsMonth, elapsedSecs)
	uptime30d, total30d := storage.SummarizeUptime(data.Rolling)

	return &Status{
		Target:            data.Target,
		TotalChecks:       totalChecks,
		TotalChecks30d:    total30d,
		UptimePctMonth:    uptimeMonth,
		UptimePct30d:      uptime30d,
		MonthStart:        monthStart.Format("2006-01-02"),
		MonthEnd:          monthEnd.Format("2006-01-02"),
		TotalSecsMonth:    totalSecsMonth,
		BudgetTotalSecs:   budget.Total,
		BudgetUsedSecs:    budget.Used,
		BudgetRemainSecs:  budget.Remain,
		BudgetRemainPct:   budget.RemainingPercent,
		BudgetAtRisk:      budget.AtRisk,
		BudgetRemainHuman: FormatDuration(budget.Remain),
		Breached:          budget.Breached,
	}, nil
}

// ComputeReport generates an SLA report for all monitors with SLA targets for a given month.
func ComputeReport(ctx context.Context, store storage.Store, year int, month time.Month, access ...storage.MonitorAccess) ([]*ReportEntry, error) {
	return computeReportAt(ctx, store, year, month, time.Now().UTC(), access...)
}

func computeReportAt(ctx context.Context, store storage.Store, year int, month time.Month, now time.Time, access ...storage.MonitorAccess) ([]*ReportEntry, error) {
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
		monitors, err := store.ListMonitors(ctx, storage.MonitorListFilter{Access: access}, storage.Pagination{Page: page, PerPage: 500})
		if err != nil {
			return nil, fmt.Errorf("list monitors: %w", err)
		}
		monList, ok := monitors.Data.([]*storage.Monitor)
		if !ok {
			return nil, fmt.Errorf("unexpected monitor list type %T", monitors.Data)
		}
		for _, m := range monList {
			if !m.HasSampledMetrics() || m.SLATarget <= 0 {
				continue
			}

			data, err := store.GetMonitorSLASnapshot(ctx, m.ID, monthStart, until, time.Time{}, access...)
			if err != nil {
				return nil, fmt.Errorf("get uptime for monitor %d: %w", m.ID, err)
			}
			if !data.Sampled || data.Target <= 0 {
				continue
			}
			var total, up, down int64
			for _, day := range data.Period {
				total += day.TotalChecks
				up += day.UpChecks
				down += day.DownChecks
			}
			uptimePct := 0.0
			if total > 0 {
				uptimePct = float64(up) / float64(total) * 100
			}
			budget := monthlyBudget(data.Target, total, up, totalSecs, elapsedSecs)

			entries = append(entries, &ReportEntry{
				MonitorID:       m.ID,
				MonitorGroupID:  m.GroupID,
				MonitorName:     m.Name,
				Target:          data.Target,
				UptimePct:       uptimePct,
				TotalChecks:     total,
				UpChecks:        up,
				DownChecks:      down,
				BudgetSecs:      budget.Total,
				UsedSecs:        budget.Used,
				RemainSecs:      budget.Remain,
				BudgetRemainPct: budget.RemainingPercent,
				BudgetAtRisk:    budget.AtRisk,
				Breached:        budget.Breached,
			})
		}
		if page >= monitors.TotalPages {
			break
		}
	}
	return entries, nil
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

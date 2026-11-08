package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/y0f/asura/internal/cron"
)

func (s *SQLiteStore) CreateMaintenanceWindow(ctx context.Context, mw *MaintenanceWindow, access ...MonitorAccess) error {
	monitorIDs, _ := json.Marshal(mw.MonitorIDs)
	now := formatTime(time.Now())
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkMaintenanceTargets(ctx, tx, mw.MonitorIDs, access); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO maintenance_windows (name, monitor_ids, start_time, end_time, recurring, cron_expr, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mw.Name, string(monitorIDs), formatTime(mw.StartTime), formatTime(mw.EndTime), mw.Recurring, mw.CronExpr, boolToInt(mw.Active), now, now)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	mw.ID = id
	mw.CreatedAt = parseTime(now)
	mw.UpdatedAt = parseTime(now)
	return nil
}

func scanMaintenanceWindow(scan func(dest ...any) error) (*MaintenanceWindow, error) {
	var mw MaintenanceWindow
	var monitorIDsStr, startTime, endTime, createdAt, updatedAt string
	if err := scan(&mw.ID, &mw.Name, &monitorIDsStr, &startTime, &endTime, &mw.Recurring, &mw.CronExpr, &mw.Active, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	mw.StartTime = parseTime(startTime)
	mw.EndTime = parseTime(endTime)
	mw.CreatedAt = parseTime(createdAt)
	mw.UpdatedAt = parseTime(updatedAt)
	if err := json.Unmarshal([]byte(monitorIDsStr), &mw.MonitorIDs); err != nil {
		return nil, fmt.Errorf("decode maintenance monitor IDs: %w", err)
	}
	return &mw, nil
}

const mwColumns = `id, name, monitor_ids, start_time, end_time, recurring, cron_expr, active, created_at, updated_at`

func (s *SQLiteStore) GetMaintenanceWindow(ctx context.Context, id int64, access ...MonitorAccess) (*MaintenanceWindow, error) {
	clause, args := maintenanceAccessSQL(access)
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+mwColumns+` FROM maintenance_windows WHERE id=?`+clause, append([]any{id}, args...)...)
	return scanMaintenanceWindow(row.Scan)
}

func (s *SQLiteStore) ListMaintenanceWindows(ctx context.Context, access ...MonitorAccess) ([]*MaintenanceWindow, error) {
	clause, args := maintenanceAccessSQL(access)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+mwColumns+` FROM maintenance_windows WHERE 1=1`+clause+` ORDER BY start_time DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []*MaintenanceWindow
	for rows.Next() {
		mw, err := scanMaintenanceWindow(rows.Scan)
		if err != nil {
			return nil, err
		}
		windows = append(windows, mw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if windows == nil {
		windows = []*MaintenanceWindow{}
	}
	return windows, nil
}

// UpdateMaintenanceWindow is the trusted/internal replacement API. Request
// handlers use UpdateMaintenanceWindowWithActions to distinguish action grants.
func (s *SQLiteStore) UpdateMaintenanceWindow(ctx context.Context, mw *MaintenanceWindow, access ...MonitorAccess) error {
	if len(access) == 0 {
		access = []MonitorAccess{{All: true}}
	}
	_, err := s.UpdateMaintenanceWindowWithActions(ctx, mw.ID, func(*MaintenanceWindow) (*MaintenanceWindow, error) { return mw, nil }, MaintenanceUpdateAccess{Update: access, Toggle: access})
	return err
}

func (s *SQLiteStore) DeleteMaintenanceWindow(ctx context.Context, id int64, access ...MonitorAccess) error {
	clause, args := maintenanceAccessSQL(access)
	result, err := s.writeDB.ExecContext(ctx, "DELETE FROM maintenance_windows WHERE id=?"+clause, append([]any{id}, args...)...)
	return scopedMutationResult(result, err, access)
}

func (s *SQLiteStore) ToggleMaintenanceWindow(ctx context.Context, id int64, active bool, access ...MonitorAccess) error {
	now := formatTime(time.Now())
	clause, args := maintenanceAccessSQL(access)
	result, err := s.writeDB.ExecContext(ctx,
		`UPDATE maintenance_windows SET active=?, updated_at=? WHERE id=?`+clause,
		append([]any{boolToInt(active), now, id}, args...)...)
	return scopedMutationResult(result, err, access)
}

func (s *SQLiteStore) IsMonitorInMaintenance(ctx context.Context, monitorID int64, at time.Time) (bool, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+mwColumns+` FROM maintenance_windows
		 WHERE recurring != '' OR end_time > ? OR active = 1`,
		formatTime(at))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		mw, err := scanMaintenanceWindow(rows.Scan)
		if err != nil {
			return false, err
		}
		if len(mw.MonitorIDs) > 0 {
			found := false
			for _, id := range mw.MonitorIDs {
				if id == monitorID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if isInWindow(mw, at) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// inRecurringWindow reports whether at falls inside an occurrence of mw that
// starts at mw.StartTime's time of day on a day accepted by onDay. Times are
// compared in UTC, the zone the window is stored in. Occurrences that began on
// earlier days are included, so a window that runs past midnight still matches
// after midnight.
func inRecurringWindow(mw *MaintenanceWindow, at time.Time, onDay func(time.Time) bool) bool {
	dur := mw.EndTime.Sub(mw.StartTime)
	if dur <= 0 {
		return false
	}
	at = at.UTC()
	h, m, sec := mw.StartTime.UTC().Clock()
	occ := time.Date(at.Year(), at.Month(), at.Day(), h, m, sec, 0, time.UTC)
	maxBack := int(dur/(24*time.Hour)) + 1
	for d := 0; d <= maxBack; d++ {
		o := occ.AddDate(0, 0, -d)
		if o.After(at) || !onDay(o) {
			continue
		}
		if at.Before(o.Add(dur)) {
			return true
		}
	}
	return false
}

func isInWindow(mw *MaintenanceWindow, at time.Time) bool {
	switch mw.Recurring {
	case "manual":
		return mw.Active
	case "cron":
		if mw.CronExpr == "" {
			return false
		}
		expr, err := cron.Parse(mw.CronExpr)
		if err != nil {
			return false
		}
		// The window covers [fire minute, fire minute + duration): look back
		// duration minutes for a matching fire time, not one more.
		dur := int(mw.EndTime.Sub(mw.StartTime).Minutes())
		for m := 0; m < dur; m++ {
			if expr.Matches(at.Add(-time.Duration(m) * time.Minute)) {
				return true
			}
		}
		return false
	case "daily":
		return inRecurringWindow(mw, at, func(time.Time) bool { return true })
	case "weekly":
		wd := mw.StartTime.UTC().Weekday()
		return inRecurringWindow(mw, at, func(d time.Time) bool { return d.Weekday() == wd })
	case "monthly":
		md := mw.StartTime.UTC().Day()
		return inRecurringWindow(mw, at, func(d time.Time) bool { return d.Day() == md })
	default:
		return !at.Before(mw.StartTime) && at.Before(mw.EndTime)
	}
}

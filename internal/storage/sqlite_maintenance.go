package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/y0f/asura/internal/cron"
)

func (s *SQLiteStore) CreateMaintenanceWindow(ctx context.Context, mw *MaintenanceWindow) error {
	monitorIDs, _ := json.Marshal(mw.MonitorIDs)
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO maintenance_windows (name, monitor_ids, start_time, end_time, recurring, cron_expr, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mw.Name, string(monitorIDs), formatTime(mw.StartTime), formatTime(mw.EndTime), mw.Recurring, mw.CronExpr, boolToInt(mw.Active), now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
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
	json.Unmarshal([]byte(monitorIDsStr), &mw.MonitorIDs)
	return &mw, nil
}

const mwColumns = `id, name, monitor_ids, start_time, end_time, recurring, cron_expr, active, created_at, updated_at`

func (s *SQLiteStore) GetMaintenanceWindow(ctx context.Context, id int64) (*MaintenanceWindow, error) {
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+mwColumns+` FROM maintenance_windows WHERE id=?`, id)
	return scanMaintenanceWindow(row.Scan)
}

func (s *SQLiteStore) ListMaintenanceWindows(ctx context.Context) ([]*MaintenanceWindow, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+mwColumns+` FROM maintenance_windows ORDER BY start_time DESC`)
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

func (s *SQLiteStore) UpdateMaintenanceWindow(ctx context.Context, mw *MaintenanceWindow) error {
	monitorIDs, _ := json.Marshal(mw.MonitorIDs)
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE maintenance_windows SET name=?, monitor_ids=?, start_time=?, end_time=?, recurring=?, cron_expr=?, active=?, updated_at=? WHERE id=?`,
		mw.Name, string(monitorIDs), formatTime(mw.StartTime), formatTime(mw.EndTime), mw.Recurring, mw.CronExpr, boolToInt(mw.Active), now, mw.ID)
	return err
}

func (s *SQLiteStore) DeleteMaintenanceWindow(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM maintenance_windows WHERE id=?", id)
	return err
}

func (s *SQLiteStore) ToggleMaintenanceWindow(ctx context.Context, id int64, active bool) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE maintenance_windows SET active=?, updated_at=? WHERE id=?`,
		boolToInt(active), now, id)
	return err
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

func timeOfDaySec(t time.Time) int {
	return t.Hour()*3600 + t.Minute()*60 + t.Second()
}

func timeInDailyWindow(mw *MaintenanceWindow, at time.Time) bool {
	duration := mw.EndTime.Sub(mw.StartTime)
	startSec := timeOfDaySec(mw.StartTime)
	atSec := timeOfDaySec(at)
	endSec := startSec + int(duration.Seconds())
	if endSec > 86400 {
		return atSec >= startSec || atSec < (endSec-86400)
	}
	return atSec >= startSec && atSec < endSec
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
		dur := int(mw.EndTime.Sub(mw.StartTime).Minutes()) + 1
		for m := 0; m < dur; m++ {
			if expr.Matches(at.Add(-time.Duration(m) * time.Minute)) {
				return true
			}
		}
		return false
	case "daily":
		return timeInDailyWindow(mw, at)
	case "weekly":
		return mw.StartTime.Weekday() == at.Weekday() && timeInDailyWindow(mw, at)
	case "monthly":
		return mw.StartTime.Day() == at.Day() && timeInDailyWindow(mw, at)
	default:
		return !at.Before(mw.StartTime) && at.Before(mw.EndTime)
	}
}

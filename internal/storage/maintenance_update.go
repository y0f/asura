package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// MaintenanceUpdate runs with the writer transaction held. It must be pure and
// must not perform I/O or call the store. Omitted fields can retain current data.
type MaintenanceUpdate func(current *MaintenanceWindow) (*MaintenanceWindow, error)

type MaintenanceUpdateAccess struct {
	Update []MonitorAccess
	Toggle []MonitorAccess
}

// UpdateMaintenanceWindowWithActions checks each action against the current and
// resulting targets. Changing active state needs toggle authority in addition to
// update authority; metadata-only changes do not.
func (s *SQLiteStore) UpdateMaintenanceWindowWithActions(ctx context.Context, id int64, prepare MaintenanceUpdate, access MaintenanceUpdateAccess) (*MaintenanceWindow, error) {
	if prepare == nil {
		return nil, fmt.Errorf("missing maintenance update")
	}
	if len(access.Update) == 0 {
		access.Update = []MonitorAccess{{}}
	}
	if len(access.Toggle) == 0 {
		access.Toggle = []MonitorAccess{{}}
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	clause, args := maintenanceAccessSQL(access.Update)
	current, err := scanMaintenanceWindow(tx.QueryRowContext(ctx, "SELECT "+mwColumns+" FROM maintenance_windows WHERE id=?"+clause, append([]any{id}, args...)...).Scan)
	if err != nil {
		return nil, err
	}
	input := *current
	input.MonitorIDs = slices.Clone(current.MonitorIDs)
	candidate, err := prepare(&input)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, fmt.Errorf("missing prepared maintenance window")
	}
	next := *candidate
	next.ID = id
	next.MonitorIDs = slices.Clone(candidate.MonitorIDs)
	if err := checkMaintenanceTargets(ctx, tx, next.MonitorIDs, access.Update); err != nil {
		return nil, err
	}
	if next.Active != current.Active {
		if err := checkMaintenanceTargets(ctx, tx, current.MonitorIDs, access.Toggle); err != nil {
			return nil, err
		}
		if err := checkMaintenanceTargets(ctx, tx, next.MonitorIDs, access.Toggle); err != nil {
			return nil, err
		}
	}
	ids, err := json.Marshal(next.MonitorIDs)
	if err != nil {
		return nil, err
	}
	next.CreatedAt = current.CreatedAt
	next.UpdatedAt = parseTime(formatTime(time.Now()))
	result, err := tx.ExecContext(ctx, `UPDATE maintenance_windows SET name=?, monitor_ids=?, start_time=?, end_time=?, recurring=?, cron_expr=?, active=?, updated_at=? WHERE id=?`, next.Name, string(ids), formatTime(next.StartTime), formatTime(next.EndTime), next.Recurring, next.CronExpr, boolToInt(next.Active), formatTime(next.UpdatedAt), id)
	if err != nil {
		return nil, err
	}
	if count, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if count != 1 {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &next, nil
}

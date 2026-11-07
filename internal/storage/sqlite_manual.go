package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RecordManualStatus authorizes against the current monitor in the same write
// transaction as its check history and visible status. Return the configuration
// used for this transition, so incident processing cannot use a stale form's
// name, thresholds, or escalation policy.
func (s *SQLiteStore) RecordManualStatus(ctx context.Context, monitorID int64, status, message string, access ...MonitorAccess) (*Monitor, error) {
	if status != "up" && status != "down" && status != "degraded" {
		return nil, fmt.Errorf("invalid manual status %q", status)
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	clause, args := monitorAccessSQL("m", access)
	args = append([]any{monitorID}, args...)
	mon, err := s.scanMonitor(tx.QueryRowContext(ctx,
		selectMonitorSQL+` WHERE m.id=? AND m.type='manual'`+clause, args...))
	if err != nil {
		return nil, err
	}
	now := formatTime(time.Now())
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO check_results (monitor_id, status, message, created_at) VALUES (?, ?, ?, ?)`,
		monitorID, status, message, now); err != nil {
		return nil, err
	}
	if err := setExplicitMonitorStatus(ctx, tx, monitorID, status, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return mon, nil
}

// Explicit manual/heartbeat transitions bypass consecutive-check thresholds,
// while preserving unrelated runtime fields such as anomaly baselines.
func setExplicitMonitorStatus(ctx context.Context, tx *sql.Tx, monitorID int64, status, now string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO monitor_status
		(monitor_id, status, last_check_at, consec_fails, consec_successes)
		SELECT id, ?, ?, CASE WHEN ?!='up' THEN failure_threshold ELSE 0 END,
			CASE WHEN ?='up' THEN success_threshold ELSE 0 END FROM monitors WHERE id=?
		ON CONFLICT(monitor_id) DO UPDATE SET status=excluded.status, last_check_at=excluded.last_check_at,
			consec_fails=excluded.consec_fails, consec_successes=excluded.consec_successes`,
		status, now, status, status, monitorID)
	return err
}

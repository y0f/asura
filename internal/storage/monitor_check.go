package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrStaleMonitorCheck means a job no longer describes an enabled, authorized
// check of the current monitor. Its result must not enter history or incidents.
var ErrStaleMonitorCheck = errors.New("monitor check configuration is no longer current")

// RecordMonitorCheck commits history and its optional visible state together,
// after rechecking the job revision and current delegation under the writer
// transaction. A nil status records an out-of-order observation without applying
// its state. Caller-owned result/status fields change only after a successful
// commit. Internal import and retention fixtures use InsertCheckResult instead.
func (s *SQLiteStore) RecordMonitorCheck(ctx context.Context, expected *Monitor, result *CheckResult, status *MonitorStatus) error {
	if expected == nil || result == nil || expected.ConfigRevision <= 0 || expected.ID <= 0 ||
		result.MonitorID != expected.ID || (status != nil && status.MonitorID != expected.ID) {
		return ErrStaleMonitorCheck
	}
	if result.AgentID != nil && *result.AgentID <= 0 {
		return ErrStaleMonitorCheck
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT settings FROM monitors WHERE id=? AND config_revision=? AND type=?
		AND enabled=1 AND type NOT IN ('manual','heartbeat')`
	args := []any{expected.ID, expected.ConfigRevision, expected.Type}
	if result.AgentID != nil {
		query += ` AND agent_enabled=1 AND type NOT IN ('docker','command') AND ` + agentMayCheck
		args = append(args, *result.AgentID)
	}
	var settings string
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&settings); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleMonitorCheck
		}
		return err
	}
	if result.AgentID != nil {
		settings, err = s.decryptSettings(settings)
		if err != nil {
			return fmt.Errorf("validate delegated check settings: %w", err)
		}
		if !agentMayReceiveSettings(settings, true) {
			return ErrStaleMonitorCheck
		}
	}
	saved := *result
	if err := insertCheckResult(ctx, tx, &saved); err != nil {
		return err
	}
	var savedStatus MonitorStatus
	if status != nil {
		savedStatus = *status
		savedStatus.ConfigRevision = expected.ConfigRevision
		if err := upsertMonitorStatus(ctx, tx, &savedStatus); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	*result = saved
	if status != nil {
		*status = savedStatus
	}
	return nil
}

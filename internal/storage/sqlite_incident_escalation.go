package storage

import (
	"context"
	"time"
)

func (s *SQLiteStore) ListPendingIncidentIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT id FROM incidents
		WHERE status IN ('open','acknowledged') AND (announcement_pending=1 OR escalation_pending=1)
		AND id>? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PrepareIncidentEscalation acknowledges pending setup in the same transaction
// that creates its state. A nil state means the monitor has no enabled steps.
// A retry cannot restart a chain that has already completed and been removed.
func (s *SQLiteStore) PrepareIncidentEscalation(ctx context.Context, incidentID int64, state *EscalationState) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT escalation_pending FROM incidents WHERE id=?", incidentID).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	if state != nil {
		_, err := tx.ExecContext(ctx, `INSERT INTO escalation_states
			(incident_id, policy_id, current_step, next_fire_at, started_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(incident_id) DO NOTHING`, incidentID, state.PolicyID, state.CurrentStep,
			formatTime(state.NextFireAt), formatTime(time.Now()))
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE incidents SET escalation_pending=0 WHERE id=?", incidentID); err != nil {
		return err
	}
	return tx.Commit()
}

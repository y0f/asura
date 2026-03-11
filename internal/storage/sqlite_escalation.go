package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) CreateEscalationPolicy(ctx context.Context, ep *EscalationPolicy) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO escalation_policies (name, description, enabled, repeat, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ep.Name, ep.Description, boolToInt(ep.Enabled), boolToInt(ep.Repeat), now, now)
	if err != nil {
		return fmt.Errorf("create escalation policy: %w", err)
	}
	id, _ := res.LastInsertId()
	ep.ID = id
	ep.CreatedAt = parseTime(now)
	ep.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetEscalationPolicy(ctx context.Context, id int64) (*EscalationPolicy, error) {
	var ep EscalationPolicy
	var createdAt, updatedAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, description, enabled, repeat, created_at, updated_at
		 FROM escalation_policies WHERE id=?`, id).
		Scan(&ep.ID, &ep.Name, &ep.Description, &ep.Enabled, &ep.Repeat, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ep.CreatedAt = parseTime(createdAt)
	ep.UpdatedAt = parseTime(updatedAt)
	return &ep, nil
}

func (s *SQLiteStore) ListEscalationPolicies(ctx context.Context) ([]*EscalationPolicy, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, description, enabled, repeat, created_at, updated_at
		 FROM escalation_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []*EscalationPolicy
	for rows.Next() {
		var ep EscalationPolicy
		var createdAt, updatedAt string
		if err := rows.Scan(&ep.ID, &ep.Name, &ep.Description, &ep.Enabled, &ep.Repeat, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		ep.CreatedAt = parseTime(createdAt)
		ep.UpdatedAt = parseTime(updatedAt)
		policies = append(policies, &ep)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if policies == nil {
		policies = []*EscalationPolicy{}
	}
	return policies, nil
}

func (s *SQLiteStore) UpdateEscalationPolicy(ctx context.Context, ep *EscalationPolicy) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE escalation_policies SET name=?, description=?, enabled=?, repeat=?, updated_at=? WHERE id=?`,
		ep.Name, ep.Description, boolToInt(ep.Enabled), boolToInt(ep.Repeat), now, ep.ID)
	return err
}

func (s *SQLiteStore) DeleteEscalationPolicy(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM escalation_policies WHERE id=?", id)
	return err
}

func (s *SQLiteStore) GetEscalationPolicySteps(ctx context.Context, policyID int64) ([]*EscalationPolicyStep, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, policy_id, step_order, delay_minutes, notification_channel_ids
		 FROM escalation_policy_steps WHERE policy_id=? ORDER BY step_order`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []*EscalationPolicyStep
	for rows.Next() {
		var st EscalationPolicyStep
		var channelIDsStr string
		if err := rows.Scan(&st.ID, &st.PolicyID, &st.StepOrder, &st.DelayMinutes, &channelIDsStr); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(channelIDsStr), &st.NotificationChannelIDs)
		if st.NotificationChannelIDs == nil {
			st.NotificationChannelIDs = []int64{}
		}
		steps = append(steps, &st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if steps == nil {
		steps = []*EscalationPolicyStep{}
	}
	return steps, nil
}

func (s *SQLiteStore) ReplaceEscalationPolicySteps(ctx context.Context, policyID int64, steps []*EscalationPolicyStep) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace escalation steps begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM escalation_policy_steps WHERE policy_id=?`, policyID); err != nil {
		return err
	}

	if len(steps) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO escalation_policy_steps (policy_id, step_order, delay_minutes, notification_channel_ids) VALUES (?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, st := range steps {
			channelIDs, _ := json.Marshal(st.NotificationChannelIDs)
			if _, err := stmt.ExecContext(ctx, policyID, st.StepOrder, st.DelayMinutes, string(channelIDs)); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// --- Escalation State ---

func (s *SQLiteStore) CreateEscalationState(ctx context.Context, state *EscalationState) error {
	now := formatTime(time.Now())
	nextFire := formatTime(state.NextFireAt)
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO escalation_states (incident_id, policy_id, current_step, next_fire_at, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		state.IncidentID, state.PolicyID, state.CurrentStep, nextFire, now)
	if err != nil {
		return fmt.Errorf("create escalation state: %w", err)
	}
	id, _ := res.LastInsertId()
	state.ID = id
	state.StartedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetEscalationStateByIncident(ctx context.Context, incidentID int64) (*EscalationState, error) {
	var st EscalationState
	var nextFire, startedAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, incident_id, policy_id, current_step, next_fire_at, started_at
		 FROM escalation_states WHERE incident_id=?`, incidentID).
		Scan(&st.ID, &st.IncidentID, &st.PolicyID, &st.CurrentStep, &nextFire, &startedAt)
	if err != nil {
		return nil, err
	}
	st.NextFireAt = parseTime(nextFire)
	st.StartedAt = parseTime(startedAt)
	return &st, nil
}

func (s *SQLiteStore) ListPendingEscalationStates(ctx context.Context, before time.Time) ([]*EscalationState, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, incident_id, policy_id, current_step, next_fire_at, started_at
		 FROM escalation_states WHERE next_fire_at <= ? ORDER BY next_fire_at`, formatTime(before))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []*EscalationState
	for rows.Next() {
		var st EscalationState
		var nextFire, startedAt string
		if err := rows.Scan(&st.ID, &st.IncidentID, &st.PolicyID, &st.CurrentStep, &nextFire, &startedAt); err != nil {
			return nil, err
		}
		st.NextFireAt = parseTime(nextFire)
		st.StartedAt = parseTime(startedAt)
		states = append(states, &st)
	}
	return states, rows.Err()
}

func (s *SQLiteStore) UpdateEscalationState(ctx context.Context, state *EscalationState) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE escalation_states SET current_step=?, next_fire_at=? WHERE id=?`,
		state.CurrentStep, formatTime(state.NextFireAt), state.ID)
	return err
}

func (s *SQLiteStore) DeleteEscalationStateByIncident(ctx context.Context, incidentID int64) error {
	_, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM escalation_states WHERE incident_id=?`, incidentID)
	return err
}

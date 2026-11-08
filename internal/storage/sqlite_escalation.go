package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type escalationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func writeEscalationPolicy(ctx context.Context, db escalationDB, ep *EscalationPolicy, create bool) error {
	now := formatTime(time.Now())
	if create {
		err := db.QueryRowContext(ctx, `INSERT INTO escalation_policies (name,description,enabled,repeat,created_at,updated_at) VALUES (?,?,?,?,?,?) RETURNING id`, ep.Name, ep.Description, boolToInt(ep.Enabled), boolToInt(ep.Repeat), now, now).Scan(&ep.ID)
		if err != nil {
			return err
		}
		ep.CreatedAt = parseTime(now)
	} else {
		res, err := db.ExecContext(ctx, `UPDATE escalation_policies SET name=?,description=?,enabled=?,repeat=?,updated_at=? WHERE id=?`, ep.Name, ep.Description, boolToInt(ep.Enabled), boolToInt(ep.Repeat), now, ep.ID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return sql.ErrNoRows
		}
	}
	ep.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLStore) CreateEscalationPolicy(ctx context.Context, ep *EscalationPolicy) error {
	return writeEscalationPolicy(ctx, s.writeDB, ep, true)
}

func getEscalationPolicy(ctx context.Context, db escalationDB, id int64) (*EscalationPolicy, error) {
	var ep EscalationPolicy
	var createdAt, updatedAt string
	err := db.QueryRowContext(ctx, `SELECT id,name,description,enabled,repeat,created_at,updated_at FROM escalation_policies WHERE id=?`, id).Scan(&ep.ID, &ep.Name, &ep.Description, &ep.Enabled, &ep.Repeat, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ep.CreatedAt = parseTime(createdAt)
	ep.UpdatedAt = parseTime(updatedAt)
	return &ep, nil
}
func (s *SQLStore) GetEscalationPolicy(ctx context.Context, id int64) (*EscalationPolicy, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return loadEscalationPolicy(ctx, tx, id)
}

func loadEscalationPolicy(ctx context.Context, db escalationDB, id int64) (*EscalationPolicy, error) {
	policy, err := getEscalationPolicy(ctx, db, id)
	if err != nil {
		return nil, err
	}
	policy.Steps, err = getEscalationPolicySteps(ctx, db, &id)
	if err != nil {
		return nil, err
	}
	return policy, nil
}

func (s *SQLStore) ListEscalationPolicies(ctx context.Context) ([]*EscalationPolicy, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,name,description,enabled,repeat,created_at,updated_at FROM escalation_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	policies := []*EscalationPolicy{}
	byID := map[int64]*EscalationPolicy{}
	for rows.Next() {
		var ep EscalationPolicy
		var createdAt, updatedAt string
		if err := rows.Scan(&ep.ID, &ep.Name, &ep.Description, &ep.Enabled, &ep.Repeat, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		ep.CreatedAt = parseTime(createdAt)
		ep.UpdatedAt = parseTime(updatedAt)
		ep.Steps = []*EscalationPolicyStep{}
		policies = append(policies, &ep)
		byID[ep.ID] = &ep
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		return policies, nil
	}
	steps, err := getEscalationPolicySteps(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	for _, step := range steps {
		if policy := byID[step.PolicyID]; policy != nil {
			policy.Steps = append(policy.Steps, step)
		}
	}
	return policies, nil
}

func (s *SQLStore) UpdateEscalationPolicy(ctx context.Context, ep *EscalationPolicy) error {
	return writeEscalationPolicy(ctx, s.writeDB, ep, false)
}
func (s *SQLStore) DeleteEscalationPolicy(ctx context.Context, id int64) error {
	res, err := s.writeDB.ExecContext(ctx, "DELETE FROM escalation_policies WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func getEscalationPolicySteps(ctx context.Context, db escalationDB, policyID *int64) ([]*EscalationPolicyStep, error) {
	query := `SELECT id,policy_id,step_order,delay_minutes,notification_channel_ids FROM escalation_policy_steps`
	var args []any
	if policyID != nil {
		query += " WHERE policy_id=?"
		args = append(args, *policyID)
	}
	rows, err := db.QueryContext(ctx, query+" ORDER BY policy_id,step_order,id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []*EscalationPolicyStep{}
	for rows.Next() {
		var st EscalationPolicyStep
		var channelIDs string
		if err := rows.Scan(&st.ID, &st.PolicyID, &st.StepOrder, &st.DelayMinutes, &channelIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(channelIDs), &st.NotificationChannelIDs); err != nil {
			return nil, fmt.Errorf("invalid stored escalation channels: %w", err)
		}
		if st.NotificationChannelIDs == nil {
			st.NotificationChannelIDs = []int64{}
		}
		steps = append(steps, &st)
	}
	return steps, rows.Err()
}
func (s *SQLStore) GetEscalationPolicySteps(ctx context.Context, id int64) ([]*EscalationPolicyStep, error) {
	return getEscalationPolicySteps(ctx, s.readDB, &id)
}

func replaceEscalationPolicySteps(ctx context.Context, tx *sqlTransaction, id int64, steps []*EscalationPolicyStep) ([]*EscalationPolicyStep, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM escalation_policy_steps WHERE policy_id=?`, id); err != nil {
		return nil, err
	}
	saved := make([]*EscalationPolicyStep, 0, len(steps))
	for i, step := range steps {
		if step == nil {
			return nil, ErrInvalidEscalationChannels
		}
		channels, err := json.Marshal(step.NotificationChannelIDs)
		if err != nil {
			return nil, err
		}
		copy := *step
		err = tx.QueryRowContext(ctx, `INSERT INTO escalation_policy_steps (policy_id,step_order,delay_minutes,notification_channel_ids) VALUES (?,?,?,?) RETURNING id`, id, i, step.DelayMinutes, string(channels)).Scan(&copy.ID)
		if err != nil {
			return nil, err
		}
		copy.PolicyID = id
		copy.StepOrder = i
		saved = append(saved, &copy)
	}
	return saved, nil
}
func (s *SQLStore) ReplaceEscalationPolicySteps(ctx context.Context, id int64, steps []*EscalationPolicyStep) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := replaceEscalationPolicySteps(ctx, tx, id, steps); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Escalation State ---

func (s *SQLiteStore) CreateEscalationState(ctx context.Context, state *EscalationState) error {
	now := formatTime(time.Now())
	nextFire := formatTime(state.NextFireAt)
	id, err := insertID(ctx, s.writeDB,
		`INSERT INTO escalation_states (incident_id, policy_id, current_step, next_fire_at, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		state.IncidentID, state.PolicyID, state.CurrentStep, nextFire, now)
	if err != nil {
		return fmt.Errorf("create escalation state: %w", err)
	}
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

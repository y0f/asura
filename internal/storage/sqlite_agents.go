package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) CreateAgent(ctx context.Context, a *Agent) error {
	token, err := generateToken()
	if err != nil {
		return err
	}
	a.Token = token
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO agents (name, location, token, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		a.Name, a.Location, token, boolToInt(a.Enabled), now)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	a.ID, _ = res.LastInsertId()
	a.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetAgent(ctx context.Context, id int64) (*Agent, error) {
	var a Agent
	var heartbeat sql.NullString
	var createdAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, location, token, last_heartbeat, enabled, created_at
		 FROM agents WHERE id=?`, id).
		Scan(&a.ID, &a.Name, &a.Location, &a.Token, &heartbeat, &a.Enabled, &createdAt)
	if err != nil {
		return nil, err
	}
	a.LastHeartbeat = parseTimePtr(heartbeat)
	a.CreatedAt = parseTime(createdAt)
	return &a, nil
}

func (s *SQLiteStore) GetAgentByToken(ctx context.Context, token string) (*Agent, error) {
	var a Agent
	var heartbeat sql.NullString
	var createdAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, location, token, last_heartbeat, enabled, created_at
		 FROM agents WHERE token=?`, token).
		Scan(&a.ID, &a.Name, &a.Location, &a.Token, &heartbeat, &a.Enabled, &createdAt)
	if err != nil {
		return nil, err
	}
	a.LastHeartbeat = parseTimePtr(heartbeat)
	a.CreatedAt = parseTime(createdAt)
	return &a, nil
}

func (s *SQLiteStore) ListAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, location, token, last_heartbeat, enabled, created_at
		 FROM agents ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var a Agent
		var heartbeat sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.Name, &a.Location, &a.Token, &heartbeat, &a.Enabled, &createdAt); err != nil {
			return nil, err
		}
		a.LastHeartbeat = parseTimePtr(heartbeat)
		a.CreatedAt = parseTime(createdAt)
		agents = append(agents, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if agents == nil {
		agents = []*Agent{}
	}
	return agents, nil
}

func (s *SQLiteStore) UpdateAgentHeartbeat(ctx context.Context, agentID int64) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE agents SET last_heartbeat=? WHERE id=?`, now, agentID)
	return err
}

func (s *SQLiteStore) DeleteAgent(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM agents WHERE id=?", id)
	return err
}

func (s *SQLiteStore) ListAgentJobs(ctx context.Context) ([]*AgentJob, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, type, target, interval_secs, timeout_secs, settings
		 FROM monitors WHERE enabled=1 AND agent_enabled=1 AND type NOT IN ('heartbeat', 'manual', 'docker', 'command')
		 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*AgentJob
	for rows.Next() {
		var j AgentJob
		var settings string
		if err := rows.Scan(&j.ID, &j.Name, &j.Type, &j.Target, &j.Interval, &j.Timeout, &settings); err != nil {
			return nil, err
		}
		if settings != "" && settings != "{}" {
			j.Settings = sanitizeSettingsForAgent([]byte(settings))
		}
		jobs = append(jobs, &j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []*AgentJob{}
	}
	return jobs, nil
}

func sanitizeSettingsForAgent(raw []byte) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	for _, secret := range []string{
		"basic_auth_pass", "bearer_token", "oauth2_client_secret",
		"mtls_client_key", "mtls_client_cert", "mtls_ca_cert", "password",
	} {
		delete(m, secret)
	}
	out, _ := json.Marshal(m)
	return out
}

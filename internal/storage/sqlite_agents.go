package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
		a.Name, a.Location, sha256Hex(token), boolToInt(a.Enabled), now)
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
		 FROM agents WHERE token=?`, sha256Hex(token)).
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

// DeleteAgent deletes agent id and disables agent checks on monitors whose
// final assignment is removed. It returns the IDs of those monitors.
func (s *SQLiteStore) DeleteAgent(ctx context.Context, id int64) ([]int64, error) {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT m.id FROM monitors m
		 JOIN monitor_agents ma ON ma.monitor_id = m.id AND ma.agent_id = ?
		 WHERE m.agent_enabled = 1
		 AND NOT EXISTS (SELECT 1 FROM monitor_agents o WHERE o.monitor_id = m.id AND o.agent_id <> ?)
		 ORDER BY m.id`, id, id)
	if err != nil {
		return nil, err
	}
	disabled := []int64{}
	for rows.Next() {
		var monID int64
		if err := rows.Scan(&monID); err != nil {
			rows.Close()
			return nil, err
		}
		disabled = append(disabled, monID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := formatTime(time.Now())
	for _, monID := range disabled {
		if _, err := tx.ExecContext(ctx,
			`UPDATE monitors SET agent_enabled=0, updated_at=? WHERE id=?`, now, monID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM agents WHERE id=?", id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return disabled, nil
}

// agentMayCheck requires a current, enabled, explicitly assigned agent.
const agentMayCheck = `EXISTS (
	SELECT 1 FROM monitor_agents ma JOIN agents a ON a.id=ma.agent_id
	WHERE ma.monitor_id=monitors.id AND ma.agent_id=? AND a.enabled=1)`

// ListAgentJobs returns only explicitly delegated monitors with settings that
// the remote checker may receive. Empty assignments never mean all agents.
func (s *SQLiteStore) ListAgentJobs(ctx context.Context, agentID int64) ([]*AgentJob, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, type, target, interval_secs, timeout_secs, settings, config_revision
		 FROM monitors WHERE enabled=1 AND agent_enabled=1 AND type NOT IN ('heartbeat', 'manual', 'docker', 'command')
		 AND `+agentMayCheck+`
		 ORDER BY id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*AgentJob
	for rows.Next() {
		var j AgentJob
		var settings string
		if err := rows.Scan(&j.ID, &j.Name, &j.Type, &j.Target, &j.Interval, &j.Timeout, &settings, &j.ConfigRevision); err != nil {
			return nil, err
		}
		settings, err = s.decryptSettings(settings)
		if err != nil {
			return nil, fmt.Errorf("decrypt settings: %w", err)
		}
		if !agentMayReceiveSettings(settings, true) {
			continue
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

// AgentMayCheckMonitor uses the same enabled/type/assignment/settings boundary
// as ListAgentJobs. Result persistence must recheck assignment and revision.
func (s *SQLiteStore) AgentMayCheckMonitor(ctx context.Context, agentID, monitorID int64) (bool, error) {
	var settings string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT settings FROM monitors WHERE id=? AND enabled=1 AND agent_enabled=1
		 AND type NOT IN ('heartbeat','manual','docker','command') AND `+agentMayCheck,
		monitorID, agentID).Scan(&settings)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	settings, err = s.decryptSettings(settings)
	if err != nil {
		return false, fmt.Errorf("decrypt settings: %w", err)
	}
	return agentMayReceiveSettings(settings, true), nil
}

// GetMonitorAgentIDs returns the agents monitorID is assigned to, in id order;
// empty means no remote agent jobs.
func (s *SQLiteStore) GetMonitorAgentIDs(ctx context.Context, monitorID int64, access ...MonitorAccess) ([]int64, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := reader.QueryContext(ctx,
		`SELECT agent_id FROM monitor_agents WHERE monitor_id = ? ORDER BY agent_id`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetMonitorAgentIDs replaces the agent assignment of monitorID. An empty list
// removes every remote agent job for this monitor.
func (s *SQLiteStore) SetMonitorAgentIDs(ctx context.Context, monitorID int64, agentIDs []int64) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_agents WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}
	for _, id := range agentIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO monitor_agents (monitor_id, agent_id) VALUES (?, ?)`, monitorID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// agentMayReceiveSettings reports whether a monitor with these (decrypted)
// settings may be checked by an agent it is offered to.
//
// Typed authentication settings remain server-only because the remote checker
// does not receive those fields. Offering such a monitor would produce a false
// "down" result. Custom request headers and bodies are
// sent verbatim and often carry credentials (Authorization, X-API-Key,
// Cookie), so they only go to agents the monitor is explicitly assigned to,
// never to every agent.
func agentMayReceiveSettings(settings string, assigned bool) bool {
	if settings == "" || settings == "{}" {
		return true
	}
	if settingsHaveAgentSecrets([]byte(settings)) {
		return false
	}
	return assigned || !settingsHaveRequestData([]byte(settings))
}

// settingsHaveRequestData reports whether the settings carry a custom request
// header or body, at the top level or in an http_multi step.
func settingsHaveRequestData(raw []byte) bool {
	type request struct {
		Headers map[string]any `json:"headers"`
		Body    any            `json:"body"`
	}
	var m struct {
		request
		Steps []request `json:"steps"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		// Settings that cannot be read cannot be shown to be credential-free.
		return true
	}
	has := func(r request) bool {
		if r.Body != nil && r.Body != "" {
			return true
		}
		for _, v := range r.Headers {
			if v != nil && v != "" {
				return true
			}
		}
		return false
	}
	if has(m.request) {
		return true
	}
	for _, st := range m.Steps {
		if has(st) {
			return true
		}
	}
	return false
}

// agentSecretKeys are monitor settings that are never sent to agents.
var agentSecretKeys = []string{
	"basic_auth_pass", "bearer_token", "oauth2_client_secret",
	"mtls_client_key", "mtls_client_cert", "mtls_ca_cert", "password",
}

// agentSecretKey matches encoding/json's case-insensitive struct field matching.
func agentSecretKey(key string) string {
	for _, secret := range agentSecretKeys {
		if strings.EqualFold(key, secret) {
			return secret
		}
	}
	return ""
}

// readAgentSettings rejects malformed/non-object settings and ambiguous duplicate
// authentication fields, including exact duplicates whose later null value can
// leave an earlier struct string intact. Raw values preserve number precision.
func readAgentSettings(raw []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, false
	}
	object := make(map[string]json.RawMessage)
	secrets := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		if _, duplicate := object[key]; duplicate {
			return nil, false
		}
		if secret := agentSecretKey(key); secret != "" {
			if secrets[secret] {
				return nil, false
			}
			secrets[secret] = true
		}
		object[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return object, true
}

// settingsHaveAgentSecrets fails closed when settings cannot be proven free of
// typed authentication material. Empty strings and null carry no credential.
func settingsHaveAgentSecrets(raw []byte) bool {
	object, valid := readAgentSettings(raw)
	if !valid {
		return true
	}
	for key, value := range object {
		if agentSecretKey(key) == "" {
			continue
		}
		var secret *string
		if err := json.Unmarshal(value, &secret); err != nil || (secret != nil && *secret != "") {
			return true
		}
	}
	return false
}

func sanitizeSettingsForAgent(raw []byte) json.RawMessage {
	object, valid := readAgentSettings(raw)
	if !valid {
		return nil
	}
	for key := range object {
		if agentSecretKey(key) != "" {
			delete(object, key)
		}
	}
	out, err := json.Marshal(object)
	if err != nil {
		return nil
	}
	return out
}

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateMonitor(ctx context.Context, m *Monitor) error {
	tags, _ := json.Marshal(m.Tags)
	if m.Settings == nil {
		m.Settings = json.RawMessage("{}")
	}
	if m.Assertions == nil {
		m.Assertions = json.RawMessage("[]")
	}
	now := formatTime(time.Now())

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create monitor begin: %w", err)
	}
	defer tx.Rollback()

	var groupID any
	if m.GroupID != nil {
		groupID = *m.GroupID
	}
	var proxyID any
	if m.ProxyID != nil {
		proxyID = *m.ProxyID
	}
	var escalationPolicyID any
	if m.EscalationPolicyID != nil {
		escalationPolicyID = *m.EscalationPolicyID
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO monitors (name, description, type, target, interval_secs, timeout_secs, enabled, tags, settings, assertions, track_changes, failure_threshold, success_threshold, upside_down, resend_interval, group_id, proxy_id, escalation_policy_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Description, m.Type, m.Target, m.Interval, m.Timeout, boolToInt(m.Enabled),
		string(tags), string(m.Settings), string(m.Assertions), boolToInt(m.TrackChanges),
		m.FailureThreshold, m.SuccessThreshold, boolToInt(m.UpsideDown), m.ResendInterval, groupID, proxyID, escalationPolicyID, now, now,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO monitor_status (monitor_id, status) VALUES (?, 'pending')`, id); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create monitor commit: %w", err)
	}

	m.ID = id
	m.CreatedAt = parseTime(now)
	m.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetMonitor(ctx context.Context, id int64) (*Monitor, error) {
	row := s.readDB.QueryRowContext(ctx,
		`SELECT m.id, m.name, m.description, m.type, m.target, m.interval_secs, m.timeout_secs, m.enabled,
		        m.tags, m.settings, m.assertions, m.track_changes, m.failure_threshold, m.success_threshold,
		        m.upside_down, m.resend_interval, m.group_id, m.proxy_id, m.escalation_policy_id, m.created_at, m.updated_at,
		        COALESCE(ms.status, 'pending'), ms.last_check_at, COALESCE(ms.consec_fails, 0), COALESCE(ms.consec_successes, 0)
		 FROM monitors m
		 LEFT JOIN monitor_status ms ON ms.monitor_id = m.id
		 WHERE m.id = ?`, id)
	return scanMonitor(row)
}

func (s *SQLiteStore) ListMonitors(ctx context.Context, f MonitorListFilter, p Pagination) (*PaginatedResult, error) {
	where := "1=1"
	var args []any

	if f.Type != "" {
		where += " AND m.type=?"
		args = append(args, f.Type)
	}
	if f.Search != "" {
		where += " AND (m.name LIKE '%' || ? || '%' OR m.target LIKE '%' || ? || '%')"
		args = append(args, f.Search, f.Search)
	}
	if f.GroupID != nil {
		where += " AND m.group_id=?"
		args = append(args, *f.GroupID)
	}
	if f.TagID != nil {
		where += " AND EXISTS (SELECT 1 FROM monitor_tags WHERE monitor_id=m.id AND tag_id=?)"
		args = append(args, *f.TagID)
	}
	if f.Status == "paused" {
		where += " AND m.enabled=0"
	} else if f.Status != "" {
		where += " AND m.enabled=1 AND COALESCE(ms.status, 'pending')=?"
		args = append(args, f.Status)
	}

	orderBy := "m.name COLLATE NOCASE ASC"
	switch f.Sort {
	case "status":
		orderBy = "COALESCE(ms.status, 'pending') ASC, m.name COLLATE NOCASE ASC"
	case "last_check":
		orderBy = "ms.last_check_at DESC, m.name COLLATE NOCASE ASC"
	case "response_time":
		orderBy = "ms.response_time DESC, m.name COLLATE NOCASE ASC"
	}

	var total int64
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM monitors m LEFT JOIN monitor_status ms ON ms.monitor_id = m.id WHERE "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	args = append(args, p.PerPage, offset)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT m.id, m.name, m.description, m.type, m.target, m.interval_secs, m.timeout_secs, m.enabled,
		        m.tags, m.settings, m.assertions, m.track_changes, m.failure_threshold, m.success_threshold,
		        m.upside_down, m.resend_interval, m.group_id, m.proxy_id, m.escalation_policy_id, m.created_at, m.updated_at,
		        COALESCE(ms.status, 'pending'), ms.last_check_at, COALESCE(ms.consec_fails, 0), COALESCE(ms.consec_successes, 0)
		 FROM monitors m
		 LEFT JOIN monitor_status ms ON ms.monitor_id = m.id
		 WHERE `+where+`
		 ORDER BY `+orderBy+`
		 LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []*Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if monitors == nil {
		monitors = []*Monitor{}
	}

	return &PaginatedResult{
		Data:       monitors,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}

func (s *SQLiteStore) UpdateMonitor(ctx context.Context, m *Monitor) error {
	tags, _ := json.Marshal(m.Tags)
	now := formatTime(time.Now())
	var groupID any
	if m.GroupID != nil {
		groupID = *m.GroupID
	}
	var proxyID any
	if m.ProxyID != nil {
		proxyID = *m.ProxyID
	}
	var escalationPolicyID any
	if m.EscalationPolicyID != nil {
		escalationPolicyID = *m.EscalationPolicyID
	}
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE monitors SET name=?, description=?, type=?, target=?, interval_secs=?, timeout_secs=?, enabled=?,
		 tags=?, settings=?, assertions=?, track_changes=?, failure_threshold=?, success_threshold=?,
		 upside_down=?, resend_interval=?, group_id=?, proxy_id=?, escalation_policy_id=?, updated_at=?
		 WHERE id=?`,
		m.Name, m.Description, m.Type, m.Target, m.Interval, m.Timeout, boolToInt(m.Enabled),
		string(tags), string(m.Settings), string(m.Assertions), boolToInt(m.TrackChanges),
		m.FailureThreshold, m.SuccessThreshold, boolToInt(m.UpsideDown), m.ResendInterval, groupID, proxyID, escalationPolicyID, now, m.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteMonitor(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM monitors WHERE id=?", id)
	return err
}

func (s *SQLiteStore) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		"UPDATE monitors SET enabled=?, updated_at=? WHERE id=?",
		boolToInt(enabled), now, id)
	return err
}

func (s *SQLiteStore) BulkSetMonitorsEnabled(ctx context.Context, ids []int64, enabled bool) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders, args := bulkArgs(ids)
	args = append([]any{boolToInt(enabled), formatTime(time.Now())}, args...)
	res, err := s.writeDB.ExecContext(ctx,
		"UPDATE monitors SET enabled=?, updated_at=? WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) BulkDeleteMonitors(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders, args := bulkArgs(ids)
	res, err := s.writeDB.ExecContext(ctx,
		"DELETE FROM monitors WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) BulkSetMonitorGroup(ctx context.Context, ids []int64, groupID *int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders, args := bulkArgs(ids)
	var gid any
	if groupID != nil {
		gid = *groupID
	}
	args = append([]any{gid, formatTime(time.Now())}, args...)
	res, err := s.writeDB.ExecContext(ctx,
		"UPDATE monitors SET group_id=?, updated_at=? WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func bulkArgs(ids []int64) (string, []any) {
	placeholders := make([]byte, 0, len(ids)*2)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	return string(placeholders), args
}

func (s *SQLiteStore) GetAllEnabledMonitors(ctx context.Context) ([]*Monitor, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT m.id, m.name, m.description, m.type, m.target, m.interval_secs, m.timeout_secs, m.enabled,
		        m.tags, m.settings, m.assertions, m.track_changes, m.failure_threshold, m.success_threshold,
		        m.upside_down, m.resend_interval, m.group_id, m.proxy_id, m.escalation_policy_id, m.created_at, m.updated_at,
		        COALESCE(ms.status, 'pending'), ms.last_check_at, COALESCE(ms.consec_fails, 0), COALESCE(ms.consec_successes, 0)
		 FROM monitors m
		 LEFT JOIN monitor_status ms ON ms.monitor_id = m.id
		 WHERE m.enabled = 1
		 ORDER BY m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []*Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return monitors, nil
}

// --- Monitor Status ---

func (s *SQLiteStore) GetMonitorStatus(ctx context.Context, monitorID int64) (*MonitorStatus, error) {
	var ms MonitorStatus
	var lastCheck sql.NullString
	err := s.readDB.QueryRowContext(ctx,
		`SELECT monitor_id, status, last_check_at, consec_fails, consec_successes, last_body_hash, last_cert_fingerprint
		 FROM monitor_status WHERE monitor_id=?`, monitorID).
		Scan(&ms.MonitorID, &ms.Status, &lastCheck, &ms.ConsecFails, &ms.ConsecSuccesses, &ms.LastBodyHash, &ms.LastCertFingerprint)
	if err != nil {
		return nil, err
	}
	ms.LastCheckAt = parseTimePtr(lastCheck)
	return &ms, nil
}

func (s *SQLiteStore) UpsertMonitorStatus(ctx context.Context, st *MonitorStatus) error {
	var lastCheck string
	if st.LastCheckAt != nil {
		lastCheck = formatTime(*st.LastCheckAt)
	}
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO monitor_status (monitor_id, status, last_check_at, consec_fails, consec_successes, last_body_hash, last_cert_fingerprint)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(monitor_id) DO UPDATE SET
		   status=excluded.status, last_check_at=excluded.last_check_at,
		   consec_fails=excluded.consec_fails, consec_successes=excluded.consec_successes,
		   last_body_hash=excluded.last_body_hash,
		   last_cert_fingerprint=excluded.last_cert_fingerprint`,
		st.MonitorID, st.Status, nullStr(lastCheck), st.ConsecFails, st.ConsecSuccesses, st.LastBodyHash, st.LastCertFingerprint)
	return err
}

// --- Check Results ---

func (s *SQLiteStore) InsertCheckResult(ctx context.Context, r *CheckResult) error {
	var certExpiry string
	if r.CertExpiry != nil {
		certExpiry = formatTime(*r.CertExpiry)
	}
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO check_results (monitor_id, status, response_time, status_code, message, headers, body, body_hash, cert_expiry, cert_fingerprint, dns_records, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.MonitorID, r.Status, r.ResponseTime, r.StatusCode, r.Message, r.Headers,
		r.Body, r.BodyHash, nullStr(certExpiry), r.CertFingerprint, r.DNSRecords, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	r.ID = id
	r.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) ListCheckResults(ctx context.Context, monitorID int64, p Pagination) (*PaginatedResult, error) {
	var total int64
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM check_results WHERE monitor_id=?", monitorID).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, monitor_id, status, response_time, status_code, message, body_hash, cert_expiry, dns_records, created_at
		 FROM check_results WHERE monitor_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		monitorID, p.PerPage, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*CheckResult
	for rows.Next() {
		var r CheckResult
		var certExp sql.NullString
		var createdAt string
		err := rows.Scan(&r.ID, &r.MonitorID, &r.Status, &r.ResponseTime, &r.StatusCode,
			&r.Message, &r.BodyHash, &certExp, &r.DNSRecords, &createdAt)
		if err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(createdAt)
		r.CertExpiry = parseTimePtr(certExp)
		results = append(results, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if results == nil {
		results = []*CheckResult{}
	}

	return &PaginatedResult{
		Data:       results,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}

func (s *SQLiteStore) GetLatestCheckResult(ctx context.Context, monitorID int64) (*CheckResult, error) {
	var r CheckResult
	var certExp sql.NullString
	var createdAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, monitor_id, status, response_time, status_code, message, body_hash, cert_expiry, dns_records, created_at
		 FROM check_results WHERE monitor_id=? ORDER BY created_at DESC LIMIT 1`, monitorID).
		Scan(&r.ID, &r.MonitorID, &r.Status, &r.ResponseTime, &r.StatusCode,
			&r.Message, &r.BodyHash, &certExp, &r.DNSRecords, &createdAt)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = parseTime(createdAt)
	r.CertExpiry = parseTimePtr(certExp)
	return &r, nil
}

func (s *SQLiteStore) GetMonitorSparklines(ctx context.Context, monitorIDs []int64, n int) (map[int64][]*SparklinePoint, error) {
	result := make(map[int64][]*SparklinePoint, len(monitorIDs))
	if len(monitorIDs) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(monitorIDs))
	args := make([]any, len(monitorIDs)+1)
	for i, id := range monitorIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(monitorIDs)] = n

	query := fmt.Sprintf(`
		SELECT monitor_id, status, response_time FROM (
			SELECT monitor_id, status, response_time,
				ROW_NUMBER() OVER (PARTITION BY monitor_id ORDER BY created_at DESC) AS rn
			FROM check_results WHERE monitor_id IN (%s)
		) t WHERE rn <= ? ORDER BY monitor_id, rn DESC`,
		strings.Join(placeholders, ","))

	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var p SparklinePoint
		var mid int64
		if err := rows.Scan(&mid, &p.Status, &p.ResponseTime); err != nil {
			return nil, err
		}
		result[mid] = append(result[mid], &p)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) GetMonitorNotificationChannelIDs(ctx context.Context, monitorID int64) ([]int64, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT channel_id FROM monitor_notifications WHERE monitor_id=? ORDER BY channel_id`, monitorID)
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

func (s *SQLiteStore) SetMonitorNotificationChannels(ctx context.Context, monitorID int64, channelIDs []int64) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set monitor notifications begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_notifications WHERE monitor_id=?`, monitorID); err != nil {
		return err
	}

	if len(channelIDs) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO monitor_notifications (monitor_id, channel_id) VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, cid := range channelIDs {
			if _, err := stmt.ExecContext(ctx, monitorID, cid); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

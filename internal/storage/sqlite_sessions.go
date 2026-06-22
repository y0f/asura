package storage

import (
	"context"
	"math"
	"strings"
	"time"
)

func (s *SQLiteStore) InsertAudit(ctx context.Context, entry *AuditEntry) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO audit_log (action, entity, entity_id, api_key_name, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.Action, entry.Entity, entry.EntityID, entry.APIKeyName, entry.Detail, now)
	return err
}

func buildAuditWhere(f AuditLogFilter) (string, []any) {
	where := "1=1"
	var args []any
	if f.Action != "" {
		where += " AND action=?"
		args = append(args, f.Action)
	}
	if f.Entity != "" {
		where += " AND entity=?"
		args = append(args, f.Entity)
	}
	if f.APIKeyName != "" {
		where += " AND api_key_name=?"
		args = append(args, f.APIKeyName)
	}
	if !f.From.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, formatTime(f.From))
	}
	if !f.To.IsZero() {
		where += " AND created_at < ?"
		args = append(args, formatTime(f.To))
	}
	return where, args
}

func (s *SQLiteStore) ListAuditLog(ctx context.Context, f AuditLogFilter, p Pagination) (*PaginatedResult, error) {
	where, args := buildAuditWhere(f)

	var total int64
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM audit_log WHERE "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	args = append(args, p.PerPage, offset)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, action, entity, entity_id, api_key_name, detail, created_at
		 FROM audit_log WHERE `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var createdAt string
		if err := rows.Scan(&e.ID, &e.Action, &e.Entity, &e.EntityID, &e.APIKeyName, &e.Detail, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(createdAt)
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []*AuditEntry{}
	}

	return &PaginatedResult{
		Data:       entries,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}

// --- Sessions ---

// --- TOTP Keys ---

func (s *SQLiteStore) CreateTOTPKey(ctx context.Context, key *TOTPKey) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO totp_keys (api_key_name, secret, created_at) VALUES (?, ?, ?)`,
		key.APIKeyName, key.Secret, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	key.ID = id
	key.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetTOTPKey(ctx context.Context, apiKeyName string) (*TOTPKey, error) {
	var key TOTPKey
	var createdAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, api_key_name, secret, created_at FROM totp_keys WHERE api_key_name=?`, apiKeyName).
		Scan(&key.ID, &key.APIKeyName, &key.Secret, &createdAt)
	if err != nil {
		return nil, err
	}
	key.CreatedAt = parseTime(createdAt)
	return &key, nil
}

func (s *SQLiteStore) DeleteTOTPKey(ctx context.Context, apiKeyName string) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM totp_keys WHERE api_key_name=?", apiKeyName)
	return err
}

func (s *SQLiteStore) GetTOTPLastCounter(ctx context.Context, apiKeyName string) (uint64, error) {
	var counter int64
	err := s.readDB.QueryRowContext(ctx,
		"SELECT last_counter FROM totp_keys WHERE api_key_name=?", apiKeyName).Scan(&counter)
	if err != nil {
		return 0, err
	}
	return uint64(counter), nil
}

func (s *SQLiteStore) UpdateTOTPLastCounter(ctx context.Context, apiKeyName string, counter uint64) error {
	_, err := s.writeDB.ExecContext(ctx,
		"UPDATE totp_keys SET last_counter=? WHERE api_key_name=?", int64(counter), apiKeyName)
	return err
}

// AdvanceTOTPCounter atomically advances last_counter to counter only if it is
// strictly greater than the stored value. It returns true when the row was
// updated (the code is fresh) and false when it was a replay/stale code. The
// compare-and-swap closes the TOCTOU window between a read and a blind write.
func (s *SQLiteStore) AdvanceTOTPCounter(ctx context.Context, apiKeyName string, counter uint64) (bool, error) {
	res, err := s.writeDB.ExecContext(ctx,
		"UPDATE totp_keys SET last_counter=? WHERE api_key_name=? AND last_counter < ?",
		int64(counter), apiKeyName, int64(counter))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// --- Sessions ---

func (s *SQLiteStore) CreateSession(ctx context.Context, sess *Session) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, api_key_name, key_hash, ip_address, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.APIKeyName, sess.KeyHash, sess.IPAddress, now, formatTime(sess.ExpiresAt))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	sess.ID = id
	sess.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	var sess Session
	var createdAt, expiresAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, token_hash, api_key_name, key_hash, ip_address, created_at, expires_at
		 FROM sessions WHERE token_hash=?`, tokenHash).
		Scan(&sess.ID, &sess.TokenHash, &sess.APIKeyName, &sess.KeyHash, &sess.IPAddress, &createdAt, &expiresAt)
	if err != nil {
		return nil, err
	}
	sess.CreatedAt = parseTime(createdAt)
	sess.ExpiresAt = parseTime(expiresAt)
	return &sess, nil
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return err
}

func (s *SQLiteStore) ExtendSession(ctx context.Context, tokenHash string, newExpiry time.Time) error {
	_, err := s.writeDB.ExecContext(ctx,
		"UPDATE sessions SET expires_at=? WHERE token_hash=?",
		formatTime(newExpiry), tokenHash)
	return err
}

func (s *SQLiteStore) DeleteSessionsByAPIKeyName(ctx context.Context, apiKeyName string) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx, "DELETE FROM sessions WHERE api_key_name=?", apiKeyName)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) DeleteSessionsExceptKeyNames(ctx context.Context, validNames []string) (int64, error) {
	if len(validNames) == 0 {
		res, err := s.writeDB.ExecContext(ctx, "DELETE FROM sessions")
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	placeholders := make([]string, len(validNames))
	args := make([]any, len(validNames))
	for i, name := range validNames {
		placeholders[i] = "?"
		args[i] = name
	}
	query := "DELETE FROM sessions WHERE api_key_name NOT IN (" + strings.Join(placeholders, ",") + ")"
	res, err := s.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Retention ---

func (s *SQLiteStore) PurgeOldData(ctx context.Context, before time.Time) (int64, error) {
	ts := formatTime(before)
	var totalDeleted int64

	res, err := s.writeDB.ExecContext(ctx, "DELETE FROM check_results WHERE created_at < ?", ts)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	totalDeleted += n

	res, err = s.writeDB.ExecContext(ctx,
		`DELETE FROM incident_events WHERE incident_id IN
		 (SELECT id FROM incidents WHERE status='resolved' AND resolved_at < ?)`, ts)
	if err != nil {
		return totalDeleted, err
	}
	n, _ = res.RowsAffected()
	totalDeleted += n

	res, err = s.writeDB.ExecContext(ctx, "DELETE FROM incidents WHERE status='resolved' AND resolved_at < ?", ts)
	if err != nil {
		return totalDeleted, err
	}
	n, _ = res.RowsAffected()
	totalDeleted += n

	res, err = s.writeDB.ExecContext(ctx, "DELETE FROM content_changes WHERE created_at < ?", ts)
	if err != nil {
		return totalDeleted, err
	}
	n, _ = res.RowsAffected()
	totalDeleted += n

	res, err = s.writeDB.ExecContext(ctx, "DELETE FROM audit_log WHERE created_at < ?", ts)
	if err != nil {
		return totalDeleted, err
	}
	n, _ = res.RowsAffected()
	totalDeleted += n

	res, err = s.writeDB.ExecContext(ctx, "DELETE FROM notification_history WHERE sent_at < ?", ts)
	if err != nil {
		return totalDeleted, err
	}
	n, _ = res.RowsAffected()
	totalDeleted += n

	expired, err := s.DeleteExpiredSessions(ctx)
	if err != nil {
		return totalDeleted, err
	}
	totalDeleted += expired

	return totalDeleted, nil
}

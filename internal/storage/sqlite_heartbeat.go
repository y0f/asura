package storage

import (
	"context"
	"database/sql"
	"time"
)

func (s *SQLiteStore) CreateHeartbeat(ctx context.Context, h *Heartbeat) error {
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO heartbeats (monitor_id, token, grace, status) VALUES (?, ?, ?, ?)`,
		h.MonitorID, sha256Hex(h.Token), h.Grace, h.Status)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	h.ID = id
	return nil
}

func (s *SQLiteStore) GetHeartbeatByToken(ctx context.Context, token string) (*Heartbeat, error) {
	var h Heartbeat
	var lastPing sql.NullString
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, monitor_id, token, grace, last_ping_at, status FROM heartbeats WHERE token=?`, sha256Hex(token)).
		Scan(&h.ID, &h.MonitorID, &h.Token, &h.Grace, &lastPing, &h.Status)
	if err != nil {
		return nil, err
	}
	h.LastPingAt = parseTimePtr(lastPing)
	return &h, nil
}

func (s *SQLiteStore) GetHeartbeatByMonitorID(ctx context.Context, monitorID int64) (*Heartbeat, error) {
	var h Heartbeat
	var lastPing sql.NullString
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, monitor_id, token, grace, last_ping_at, status FROM heartbeats WHERE monitor_id=?`, monitorID).
		Scan(&h.ID, &h.MonitorID, &h.Token, &h.Grace, &lastPing, &h.Status)
	if err != nil {
		return nil, err
	}
	h.LastPingAt = parseTimePtr(lastPing)
	return &h, nil
}

// UpdateHeartbeatPing records the ping and its visible status in one transaction.
// Binding the token to the expected monitor also protects a lookup racing deletion
// and recreation of a heartbeat.
func (s *SQLiteStore) UpdateHeartbeatPing(ctx context.Context, monitorID int64, token string) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTime(time.Now())
	var id int64
	err = tx.QueryRowContext(ctx,
		`UPDATE heartbeats SET last_ping_at=?, status='up' WHERE monitor_id=? AND token=? RETURNING monitor_id`,
		now, monitorID, sha256Hex(token)).Scan(&id)
	if err != nil {
		return err
	}
	if err := setHeartbeatMonitorStatus(ctx, tx, id, "up", now); err != nil {
		return err
	}
	return tx.Commit()
}

// ExpireHeartbeat rechecks the current deadline in the same write transaction as
// the transition. A ping after the watcher's scan cannot be overwritten, even
// when two pings have the same stored timestamp.
func (s *SQLiteStore) ExpireHeartbeat(ctx context.Context, monitorID int64) (bool, error) {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := formatTime(time.Now())
	res, err := tx.ExecContext(ctx, `UPDATE heartbeats SET status='down'
		WHERE monitor_id=? AND status!='down' AND last_ping_at IS NOT NULL
		AND EXISTS (SELECT 1 FROM monitors m WHERE m.id=heartbeats.monitor_id AND m.enabled=1
			AND datetime(heartbeats.last_ping_at, '+' || (m.interval_secs + heartbeats.grace) || ' seconds') < datetime(?))`,
		monitorID, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if err := setHeartbeatMonitorStatus(ctx, tx, monitorID, "down", now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func setHeartbeatMonitorStatus(ctx context.Context, tx *sql.Tx, monitorID int64, status, now string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO monitor_status
		(monitor_id, status, last_check_at, consec_fails, consec_successes)
		SELECT id, ?, ?, CASE WHEN ?='down' THEN failure_threshold ELSE 0 END,
			CASE WHEN ?='up' THEN success_threshold ELSE 0 END FROM monitors WHERE id=?
		ON CONFLICT(monitor_id) DO UPDATE SET status=excluded.status, last_check_at=excluded.last_check_at,
			consec_fails=excluded.consec_fails, consec_successes=excluded.consec_successes`,
		status, now, status, status, monitorID)
	return err
}

func (s *SQLiteStore) ListExpiredHeartbeats(ctx context.Context) ([]*Heartbeat, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT h.id, h.monitor_id, h.token, h.grace, h.last_ping_at, h.status
		 FROM heartbeats h
		 JOIN monitors m ON m.id = h.monitor_id
		 WHERE m.enabled = 1
		   AND h.last_ping_at IS NOT NULL
		   AND datetime(h.last_ping_at, '+' || (m.interval_secs + h.grace) || ' seconds') < datetime('now')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var heartbeats []*Heartbeat
	for rows.Next() {
		var h Heartbeat
		var lastPing sql.NullString
		if err := rows.Scan(&h.ID, &h.MonitorID, &h.Token, &h.Grace, &lastPing, &h.Status); err != nil {
			return nil, err
		}
		h.LastPingAt = parseTimePtr(lastPing)
		heartbeats = append(heartbeats, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return heartbeats, nil
}

func (s *SQLiteStore) UpdateHeartbeatStatus(ctx context.Context, monitorID int64, status string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE heartbeats SET status=? WHERE monitor_id=?`, status, monitorID)
	return err
}

func (s *SQLiteStore) DeleteHeartbeat(ctx context.Context, monitorID int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM heartbeats WHERE monitor_id=?", monitorID)
	return err
}

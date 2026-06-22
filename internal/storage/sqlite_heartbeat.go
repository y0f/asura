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

func (s *SQLiteStore) UpdateHeartbeatPing(ctx context.Context, token string) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE heartbeats SET last_ping_at=?, status='up' WHERE token=?`, now, sha256Hex(token))
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

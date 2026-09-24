package storage

import (
	"context"
	"time"
)

func (s *SQLiteStore) CreateMonitorGroup(ctx context.Context, g *MonitorGroup) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO monitor_groups (name, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		g.Name, g.SortOrder, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	g.ID = id
	g.CreatedAt = parseTime(now)
	g.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetMonitorGroup(ctx context.Context, id int64) (*MonitorGroup, error) {
	var g MonitorGroup
	var createdAt, updatedAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, sort_order, created_at, updated_at FROM monitor_groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &g.SortOrder, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	g.CreatedAt = parseTime(createdAt)
	g.UpdatedAt = parseTime(updatedAt)
	return &g, nil
}

func (s *SQLiteStore) ListMonitorGroups(ctx context.Context) ([]*MonitorGroup, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, sort_order, created_at, updated_at FROM monitor_groups ORDER BY sort_order, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*MonitorGroup
	for rows.Next() {
		var g MonitorGroup
		var createdAt, updatedAt string
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		g.CreatedAt = parseTime(createdAt)
		g.UpdatedAt = parseTime(updatedAt)
		groups = append(groups, &g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []*MonitorGroup{}
	}
	return groups, nil
}

func (s *SQLiteStore) UpdateMonitorGroup(ctx context.Context, g *MonitorGroup) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE monitor_groups SET name=?, sort_order=?, updated_at=? WHERE id=?`,
		g.Name, g.SortOrder, now, g.ID)
	return err
}

func (s *SQLiteStore) DeleteMonitorGroup(ctx context.Context, id int64) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE monitors SET group_id=NULL WHERE group_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM monitor_groups WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

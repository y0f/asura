package storage

import (
	"context"
	"fmt"
	"time"
)

func (s *SQLiteStore) CreateTag(ctx context.Context, t *Tag) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO tags (name, color, created_at) VALUES (?, ?, ?)`,
		t.Name, t.Color, now)
	if err != nil {
		return fmt.Errorf("create tag: %w", err)
	}
	id, _ := res.LastInsertId()
	t.ID = id
	t.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetTag(ctx context.Context, id int64) (*Tag, error) {
	var t Tag
	var createdAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Color, &createdAt)
	if err != nil {
		return nil, err
	}
	t.CreatedAt = parseTime(createdAt)
	return &t, nil
}

func (s *SQLiteStore) GetTagByName(ctx context.Context, name string) (*Tag, error) {
	var t Tag
	var createdAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE name = ?`, name).
		Scan(&t.ID, &t.Name, &t.Color, &createdAt)
	if err != nil {
		return nil, err
	}
	t.CreatedAt = parseTime(createdAt)
	return &t, nil
}

func (s *SQLiteStore) ListTags(ctx context.Context) ([]*Tag, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, color, created_at FROM tags ORDER BY name COLLATE NOCASE ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*Tag
	for rows.Next() {
		var t Tag
		var createdAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &createdAt); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTime(createdAt)
		tags = append(tags, &t)
	}
	if tags == nil {
		tags = []*Tag{}
	}
	return tags, rows.Err()
}

func (s *SQLiteStore) UpdateTag(ctx context.Context, t *Tag) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE tags SET name = ?, color = ? WHERE id = ?`,
		t.Name, t.Color, t.ID)
	return err
}

func (s *SQLiteStore) DeleteTag(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SetMonitorTags(ctx context.Context, monitorID int64, tags []MonitorTag) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set monitor tags begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_tags WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}

	if len(tags) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO monitor_tags (monitor_id, tag_id, value) VALUES (?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, t := range tags {
			if _, err := stmt.ExecContext(ctx, monitorID, t.TagID, t.Value); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) GetMonitorTags(ctx context.Context, monitorID int64) ([]MonitorTag, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT mt.tag_id, t.name, t.color, mt.value
		 FROM monitor_tags mt
		 JOIN tags t ON t.id = mt.tag_id
		 WHERE mt.monitor_id = ?
		 ORDER BY t.name COLLATE NOCASE ASC`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []MonitorTag
	for rows.Next() {
		var mt MonitorTag
		if err := rows.Scan(&mt.TagID, &mt.Name, &mt.Color, &mt.Value); err != nil {
			return nil, err
		}
		tags = append(tags, mt)
	}
	if tags == nil {
		tags = []MonitorTag{}
	}
	return tags, rows.Err()
}

func (s *SQLiteStore) GetMonitorTagsBatch(ctx context.Context, monitorIDs []int64) (map[int64][]MonitorTag, error) {
	if len(monitorIDs) == 0 {
		return map[int64][]MonitorTag{}, nil
	}

	placeholders, args := bulkArgs(monitorIDs)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT mt.monitor_id, mt.tag_id, t.name, t.color, mt.value
		 FROM monitor_tags mt
		 JOIN tags t ON t.id = mt.tag_id
		 WHERE mt.monitor_id IN (`+placeholders+`)
		 ORDER BY t.name COLLATE NOCASE ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]MonitorTag)
	for rows.Next() {
		var monID int64
		var mt MonitorTag
		if err := rows.Scan(&monID, &mt.TagID, &mt.Name, &mt.Color, &mt.Value); err != nil {
			return nil, err
		}
		result[monID] = append(result[monID], mt)
	}
	return result, rows.Err()
}

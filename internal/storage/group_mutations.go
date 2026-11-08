package storage

import (
	"context"
	"database/sql"
	"errors"
)

var ErrGroupNotEmpty = errors.New("move the group's monitors before deleting it")

// DeleteEmptyMonitorGroup is the request-facing deletion policy. Deleting a
// group must not silently move monitors around their independently scoped
// update/assignment permissions. Admission and deletion share the writer lock.
func (s *SQLiteStore) DeleteEmptyMonitorGroup(ctx context.Context, id int64) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM monitor_groups WHERE id=?)", id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	var occupied bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM monitors WHERE group_id=?)", id).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return ErrGroupNotEmpty
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM monitor_groups WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

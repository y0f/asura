package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type NotificationUpdate func(current *NotificationChannel) (*NotificationChannel, error)

const notificationColumns = "id, name, type, enabled, settings, events, created_at, updated_at"

func (s *SQLiteStore) scanNotificationChannel(scan func(...any) error) (*NotificationChannel, error) {
	var ch NotificationChannel
	var settings, events, createdAt, updatedAt string
	if err := scan(&ch.ID, &ch.Name, &ch.Type, &ch.Enabled, &settings, &events, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	decrypted, err := s.decryptRequiredSecret(settings)
	if err != nil {
		return nil, fmt.Errorf("decrypt notification settings: %w", err)
	}
	ch.Settings = json.RawMessage(decrypted)
	ch.CreatedAt, ch.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	if err := json.Unmarshal([]byte(events), &ch.Events); err != nil {
		return nil, fmt.Errorf("invalid stored notification events")
	}
	return &ch, nil
}

// UpdateNotificationChannelWith runs a pure preparation callback against the
// latest writer-transaction row and commits metadata and credentials together.
// The callback must not perform I/O or call the store.
func (s *SQLiteStore) UpdateNotificationChannelWith(ctx context.Context, id int64, prepare NotificationUpdate) (*NotificationChannel, error) {
	if prepare == nil {
		return nil, fmt.Errorf("missing notification update")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := s.scanNotificationChannel(tx.QueryRowContext(ctx, "SELECT "+notificationColumns+" FROM notification_channels WHERE id=?", id).Scan)
	if err != nil {
		return nil, err
	}
	input := *current
	input.Settings, input.Events = slices.Clone(current.Settings), slices.Clone(current.Events)
	candidate, err := prepare(&input)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, fmt.Errorf("missing prepared notification channel")
	}
	next := *candidate
	next.Settings, next.Events = slices.Clone(candidate.Settings), slices.Clone(candidate.Events)
	next.ID, next.CreatedAt = id, current.CreatedAt
	next.UpdatedAt = parseTime(formatTime(time.Now()))
	settings, err := s.encryptSettings(string(next.Settings))
	if err != nil {
		return nil, err
	}
	events, err := json.Marshal(next.Events)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE notification_channels SET name=?,type=?,enabled=?,settings=?,events=?,updated_at=? WHERE id=?`, next.Name, next.Type, boolToInt(next.Enabled), settings, string(events), formatTime(next.UpdatedAt), id)
	if err != nil {
		return nil, err
	}
	if count, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if count != 1 {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &next, nil
}

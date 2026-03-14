package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) CreateNotificationChannel(ctx context.Context, ch *NotificationChannel) error {
	events, _ := json.Marshal(ch.Events)
	now := formatTime(time.Now())
	settings, err := s.encryptSettings(string(ch.Settings))
	if err != nil {
		return fmt.Errorf("encrypt settings: %w", err)
	}
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO notification_channels (name, type, enabled, settings, events, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.Name, ch.Type, boolToInt(ch.Enabled), settings, string(events), now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	ch.ID = id
	ch.CreatedAt = parseTime(now)
	ch.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetNotificationChannel(ctx context.Context, id int64) (*NotificationChannel, error) {
	var ch NotificationChannel
	var settingsStr, eventsStr, createdAt, updatedAt string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, type, enabled, settings, events, created_at, updated_at
		 FROM notification_channels WHERE id=?`, id).
		Scan(&ch.ID, &ch.Name, &ch.Type, &ch.Enabled, &settingsStr, &eventsStr, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	decrypted, err := s.decryptSettings(settingsStr)
	if err != nil {
		return nil, fmt.Errorf("decrypt settings: %w", err)
	}
	ch.Settings = json.RawMessage(decrypted)
	ch.CreatedAt = parseTime(createdAt)
	ch.UpdatedAt = parseTime(updatedAt)
	json.Unmarshal([]byte(eventsStr), &ch.Events)
	return &ch, nil
}

func (s *SQLiteStore) ListNotificationChannels(ctx context.Context) ([]*NotificationChannel, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, type, enabled, settings, events, created_at, updated_at
		 FROM notification_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*NotificationChannel
	for rows.Next() {
		var ch NotificationChannel
		var settingsStr, eventsStr, createdAt, updatedAt string
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Type, &ch.Enabled, &settingsStr, &eventsStr, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		decrypted, err := s.decryptSettings(settingsStr)
		if err != nil {
			return nil, fmt.Errorf("decrypt settings: %w", err)
		}
		ch.Settings = json.RawMessage(decrypted)
		ch.CreatedAt = parseTime(createdAt)
		ch.UpdatedAt = parseTime(updatedAt)
		json.Unmarshal([]byte(eventsStr), &ch.Events)
		channels = append(channels, &ch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if channels == nil {
		channels = []*NotificationChannel{}
	}
	return channels, nil
}

func (s *SQLiteStore) UpdateNotificationChannel(ctx context.Context, ch *NotificationChannel) error {
	events, _ := json.Marshal(ch.Events)
	now := formatTime(time.Now())
	settings, err := s.encryptSettings(string(ch.Settings))
	if err != nil {
		return fmt.Errorf("encrypt settings: %w", err)
	}
	_, err = s.writeDB.ExecContext(ctx,
		`UPDATE notification_channels SET name=?, type=?, enabled=?, settings=?, events=?, updated_at=? WHERE id=?`,
		ch.Name, ch.Type, boolToInt(ch.Enabled), settings, string(events), now, ch.ID)
	return err
}

func (s *SQLiteStore) DeleteNotificationChannel(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM notification_channels WHERE id=?", id)
	return err
}

func (s *SQLiteStore) encryptSettings(settings string) (string, error) {
	if s.enc == nil {
		return settings, nil
	}
	return s.enc.Encrypt(settings)
}

func (s *SQLiteStore) decryptSettings(settings string) (string, error) {
	if s.enc == nil {
		return settings, nil
	}
	return s.enc.Decrypt(settings)
}

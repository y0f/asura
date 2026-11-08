package storage

import (
	"context"
	"database/sql"
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
	id, err := insertID(ctx, s.writeDB,
		`INSERT INTO notification_channels (name, type, enabled, settings, events, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.Name, ch.Type, boolToInt(ch.Enabled), settings, string(events), now, now)
	if err != nil {
		return err
	}
	ch.ID = id
	ch.CreatedAt = parseTime(now)
	ch.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetNotificationChannel(ctx context.Context, id int64) (*NotificationChannel, error) {
	return s.scanNotificationChannel(s.readDB.QueryRowContext(ctx, "SELECT "+notificationColumns+" FROM notification_channels WHERE id=?", id).Scan)
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
		ch, err := s.scanNotificationChannel(rows.Scan)
		if err != nil {
			return nil, err
		}
		channels = append(channels, ch)
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
	_, err := s.UpdateNotificationChannelWith(ctx, ch.ID, func(*NotificationChannel) (*NotificationChannel, error) { return ch, nil })
	return err
}

func (s *SQLiteStore) DeleteNotificationChannel(ctx context.Context, id int64) error {
	result, err := s.writeDB.ExecContext(ctx, "DELETE FROM notification_channels WHERE id=?", id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return sql.ErrNoRows
	}
	return nil
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

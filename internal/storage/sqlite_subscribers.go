package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (s *SQLiteStore) CreateStatusPageSubscriber(ctx context.Context, sub *StatusPageSubscriber) error {
	token, err := generateToken()
	if err != nil {
		return err
	}
	sub.Token = token

	confirmed := boolToInt(sub.Confirmed)
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO status_page_subscribers (status_page_id, type, email, webhook_url, confirmed, token)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sub.StatusPageID, sub.Type, sub.Email, sub.WebhookURL, confirmed, token)
	if err != nil {
		return fmt.Errorf("create subscriber: %w", err)
	}
	sub.ID, _ = res.LastInsertId()
	return nil
}

func (s *SQLiteStore) GetSubscriberByToken(ctx context.Context, token string) (*StatusPageSubscriber, error) {
	row := s.readDB.QueryRowContext(ctx,
		`SELECT id, status_page_id, type, email, webhook_url, confirmed, token, created_at
		 FROM status_page_subscribers WHERE token = ?`, token)
	return scanSubscriber(row)
}

func (s *SQLiteStore) ConfirmSubscriber(ctx context.Context, token string) error {
	res, err := s.writeDB.ExecContext(ctx,
		`UPDATE status_page_subscribers SET confirmed = 1 WHERE token = ? AND confirmed = 0`, token)
	if err != nil {
		return fmt.Errorf("confirm subscriber: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("subscriber not found or already confirmed")
	}
	return nil
}

func (s *SQLiteStore) DeleteSubscriberByToken(ctx context.Context, token string) error {
	res, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM status_page_subscribers WHERE token = ?`, token)
	if err != nil {
		return fmt.Errorf("delete subscriber: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("subscriber not found")
	}
	return nil
}

func (s *SQLiteStore) CountSubscribersByPage(ctx context.Context, pageID int64) (int64, error) {
	var count int64
	err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM status_page_subscribers WHERE status_page_id = ? AND confirmed = 1`,
		pageID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count subscribers: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) ListConfirmedSubscribers(ctx context.Context, pageID int64) ([]*StatusPageSubscriber, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, status_page_id, type, email, webhook_url, confirmed, token, created_at
		 FROM status_page_subscribers
		 WHERE status_page_id = ? AND confirmed = 1`, pageID)
	if err != nil {
		return nil, fmt.Errorf("list confirmed subscribers: %w", err)
	}
	defer rows.Close()

	var subs []*StatusPageSubscriber
	for rows.Next() {
		sub, err := scanSubscriberRow(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *SQLiteStore) DeleteSubscriber(ctx context.Context, id int64) error {
	res, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM status_page_subscribers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete subscriber: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("subscriber not found")
	}
	return nil
}

func (s *SQLiteStore) GetStatusPageIDsForMonitor(ctx context.Context, monitorID int64) ([]int64, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT DISTINCT spm.page_id
		 FROM status_page_monitors spm
		 JOIN status_pages sp ON sp.id = spm.page_id
		 WHERE spm.monitor_id = ? AND sp.enabled = 1`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("get status page ids for monitor: %w", err)
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

func scanSubscriber(row *sql.Row) (*StatusPageSubscriber, error) {
	var sub StatusPageSubscriber
	var confirmed int
	var createdAt string
	err := row.Scan(&sub.ID, &sub.StatusPageID, &sub.Type, &sub.Email, &sub.WebhookURL, &confirmed, &sub.Token, &createdAt)
	if err != nil {
		return nil, err
	}
	sub.Confirmed = confirmed == 1
	sub.CreatedAt = parseTime(createdAt)
	return &sub, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSubscriberRow(row rowScanner) (*StatusPageSubscriber, error) {
	var sub StatusPageSubscriber
	var confirmed int
	var createdAt string
	err := row.Scan(&sub.ID, &sub.StatusPageID, &sub.Type, &sub.Email, &sub.WebhookURL, &confirmed, &sub.Token, &createdAt)
	if err != nil {
		return nil, err
	}
	sub.Confirmed = confirmed == 1
	sub.CreatedAt = parseTime(createdAt)
	return &sub, nil
}

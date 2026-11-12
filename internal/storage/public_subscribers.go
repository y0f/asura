package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MaxSubscribersPerPage bounds the public, unauthenticated subscription table
// growth and the notification fan-out of a single status page.
const MaxSubscribersPerPage = 10000

// UnconfirmedSubscriberLifetime is how long an email subscription waits for
// confirmation before retention removes it.
const UnconfirmedSubscriberLifetime = 24 * time.Hour

var (
	// ErrSubscriberExists reports that the same address or webhook is already
	// subscribed (or awaiting confirmation) on the page. Callers answer it like
	// a success so the response does not reveal existing subscribers.
	ErrSubscriberExists = errors.New("subscriber already exists")
	// ErrSubscriberLimit reports that the page reached MaxSubscribersPerPage.
	ErrSubscriberLimit = errors.New("status page subscriber limit reached")
)

// CreatePublicStatusPageSubscriber rechecks publication and the authenticated
// page credential in the same writer transaction which admits the subscriber.
// Returned branding is from that transaction, for confirmation email/redirects.
func (s *SQLStore) CreatePublicStatusPageSubscriber(ctx context.Context, sub *StatusPageSubscriber, access PublicStatusPageAccess) (*StatusPage, error) {
	if access.Audience != PublicStatusPageWebSubscription && access.Audience != PublicStatusPageAPISubscription {
		return nil, sql.ErrNoRows
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	page, err := publicStatusPageQuery(ctx, tx, sub.StatusPageID, access)
	if err != nil {
		return nil, err
	}
	var duplicates, total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_subscribers
		WHERE status_page_id=? AND type=? AND lower(email)=lower(?) AND webhook_url=?`,
		sub.StatusPageID, sub.Type, sub.Email, sub.WebhookURL).Scan(&duplicates); err != nil {
		return nil, err
	}
	if duplicates > 0 {
		return page, ErrSubscriberExists
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_subscribers WHERE status_page_id=?`,
		sub.StatusPageID).Scan(&total); err != nil {
		return nil, err
	}
	if total >= MaxSubscribersPerPage {
		return nil, ErrSubscriberLimit
	}
	created, err := insertSubscriber(ctx, tx, sub)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	*sub = *created
	return page, nil
}

// ConfirmPageSubscriber is idempotent for an existing token on this enabled
// page. A bearer token authorizes only its own subscription, never another page.
func (s *SQLStore) ConfirmPageSubscriber(ctx context.Context, pageID int64, token string) error {
	result, err := s.writeDB.ExecContext(ctx, `UPDATE status_page_subscribers SET confirmed=1
		WHERE status_page_id=? AND token=? AND EXISTS (
			SELECT 1 FROM status_pages WHERE id=? AND enabled=1)`, pageID, token, pageID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

// DeletePageSubscriberByToken remains available after publication is disabled.
func (s *SQLStore) DeletePageSubscriberByToken(ctx context.Context, pageID int64, token string) error {
	result, err := s.writeDB.ExecContext(ctx, "DELETE FROM status_page_subscribers WHERE status_page_id=? AND token=?", pageID, token)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

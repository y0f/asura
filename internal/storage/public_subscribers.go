package storage

import (
	"context"
	"database/sql"
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

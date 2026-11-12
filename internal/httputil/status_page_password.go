package httputil

import (
	"context"
	"log/slog"

	"github.com/y0f/asura/v2/internal/pageauth"
	"github.com/y0f/asura/v2/internal/storage"
)

type statusPagePasswordUpgrader interface {
	UpgradeStatusPagePasswordHash(ctx context.Context, id int64, oldHash, newHash string) (bool, error)
}

var _ statusPagePasswordUpgrader = storage.Store(nil)

// UpgradeStatusPagePassword replaces a verified legacy SHA-256 page password
// hash with a salted PBKDF2 hash of the same password and returns the hash now
// current for the page. It must only be called after password verified
// against current. On any failure the current hash stays in use.
func UpgradeStatusPagePassword(ctx context.Context, store statusPagePasswordUpgrader, logger *slog.Logger, pageID int64, current, password string) string {
	if !pageauth.NeedsRehash(current) {
		return current
	}
	upgraded, err := pageauth.Hash(password)
	if err != nil {
		return current
	}
	ok, err := store.UpgradeStatusPagePasswordHash(ctx, pageID, current, upgraded)
	if err != nil {
		logger.Error("upgrade status page password hash", "status_page_id", pageID, "error", err)
		return current
	}
	if !ok {
		return current
	}
	return upgraded
}

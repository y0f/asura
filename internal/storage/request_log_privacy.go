package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/y0f/asura/v3/internal/logsafe"
)

const requestLogPrivacyKey = "request_log_privacy_v1"

// FinalizeRequestLogPrivacy runs before Open exposes the store. Canonicalizing
// stored metadata (also enforced on insertion) makes SQL group sanitized paths
// before applying aggregate limits, preserving every request and rollup count.
// The marker and all changes commit together, so interruption retries safely.
func (s *SQLStore) FinalizeRequestLogPrivacy(ctx context.Context) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var marker string
	err = tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", requestLogPrivacyKey).Scan(&marker)
	if err == nil && marker == "1" {
		return tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	type metadata struct {
		id            int64
		path, referer string
	}
	var after int64
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id, path, referer FROM request_logs WHERE id>? ORDER BY id LIMIT 128", after)
		if err != nil {
			return err
		}
		batch := make([]metadata, 0, 128)
		for rows.Next() {
			var row metadata
			if err := rows.Scan(&row.id, &row.path, &row.referer); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		// Close the reader before issuing writes on the same connection.
		for _, row := range batch {
			cleanPath, cleanReferer := logsafe.Path(row.path), logsafe.Referer(row.referer)
			if cleanPath != row.path || cleanReferer != row.referer {
				if _, err := tx.ExecContext(ctx, "UPDATE request_logs SET path=?, referer=? WHERE id=?", cleanPath, cleanReferer, row.id); err != nil {
					return err
				}
			}
			after = row.id
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO meta (key,value) VALUES (?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value", requestLogPrivacyKey, "1")
	if err != nil {
		return err
	}
	return tx.Commit()
}

package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/y0f/asura/v3/internal/model"
)

var ErrInvalidMonitorProxy = errors.New("the proxy protocol is incompatible with the monitor configuration; DNS proxies require an explicit DNS server")
var ErrProxyInUse = errors.New("the proxy is assigned to monitors; change their routing before deleting it")

// Validate the current proxy in the same writer transaction as the monitor.
// A proxy may be disabled intentionally, but must never be silently ignored.
func checkMonitorProxy(ctx context.Context, tx *sqlTransaction, m *Monitor) error {
	if m.ProxyID == nil {
		return nil
	}
	var protocol string
	if err := tx.QueryRowContext(ctx, "SELECT protocol FROM proxies WHERE id=?", *m.ProxyID).Scan(&protocol); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidMonitorRelation
		}
		return err
	}
	if err := model.ValidateProxyCompatibility(m, protocol); err != nil {
		return ErrInvalidMonitorProxy
	}
	return nil
}

// Proxy owners need not be able to read the assigned monitors. Return only a
// generic compatibility error, without their IDs, names or settings.
func (s *SQLiteStore) checkProxyMonitors(ctx context.Context, tx *sqlTransaction, id int64, protocol string) error {
	rows, err := tx.QueryContext(ctx, "SELECT type, settings FROM monitors WHERE proxy_id=?", id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m Monitor
		var settings string
		if err := rows.Scan(&m.Type, &settings); err != nil {
			return err
		}
		if m.Type == "dns" {
			plaintext, err := s.decryptRequiredSecret(settings)
			if err != nil {
				return err
			}
			m.Settings = []byte(plaintext)
		}
		if err := model.ValidateProxyCompatibility(&m, protocol); err != nil {
			return ErrInvalidMonitorProxy
		}
	}
	return rows.Err()
}

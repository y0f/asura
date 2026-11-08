package storage

import (
	"context"
	"database/sql"
)

type monitorReadQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func authorizeMonitorRead(ctx context.Context, q monitorReadQuery, id int64, access ...MonitorAccess) error {
	if len(access) == 0 {
		return nil
	}
	clause, args := monitorAccessSQL("m", access)
	var present int
	return q.QueryRowContext(ctx, "SELECT 1 FROM monitors m WHERE m.id=?"+clause, append([]any{id}, args...)...).Scan(&present)
}

// monitorReader binds authorization and all related reads to the same SQLite
// snapshot. Trusted internal calls omit filters and keep the direct pool path.
// Request callers must pass explicit filters, including an empty deny filter.
func (s *SQLiteStore) monitorReader(ctx context.Context, id int64, access ...MonitorAccess) (monitorReadQuery, func(), error) {
	if len(access) == 0 {
		return s.readDB, func() {}, nil
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	if err := authorizeMonitorRead(ctx, tx, id, access...); err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	return tx, func() { tx.Rollback() }, nil
}

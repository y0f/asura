package storage

import (
	"context"
	"database/sql"
)

type insertIDExecutor interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// insertID consumes the generated key from the same statement that creates the
// row. Both supported databases implement RETURNING; PostgreSQL intentionally
// does not implement database/sql.Result.LastInsertId.
func insertID(ctx context.Context, db insertIDExecutor, query string, args ...any) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, query+" RETURNING id", args...).Scan(&id)
	return id, err
}

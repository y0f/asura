package storage

import (
	"context"
	"database/sql"
)

type sqlDialect uint8

const (
	sqliteDialect sqlDialect = iota
	postgresDialect
)

func (dialect sqlDialect) bind(query string) string {
	if dialect == postgresDialect {
		return postgresBind(query)
	}
	return query
}

// sqlPool preserves database/sql's connection and statement management. Only
// positional parameter notation and explicit read-snapshot isolation differ.
type sqlPool struct {
	*sql.DB
	dialect sqlDialect
}

func (pool *sqlPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return pool.DB.ExecContext(ctx, pool.dialect.bind(query), args...)
}

func (pool *sqlPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return pool.DB.QueryContext(ctx, pool.dialect.bind(query), args...)
}

func (pool *sqlPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return pool.DB.QueryRowContext(ctx, pool.dialect.bind(query), args...)
}

func (pool *sqlPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return pool.DB.PrepareContext(ctx, pool.dialect.bind(query))
}

func (pool *sqlPool) Exec(query string, args ...any) (sql.Result, error) {
	return pool.ExecContext(context.Background(), query, args...)
}

func (pool *sqlPool) Query(query string, args ...any) (*sql.Rows, error) {
	return pool.QueryContext(context.Background(), query, args...)
}

func (pool *sqlPool) QueryRow(query string, args ...any) *sql.Row {
	return pool.QueryRowContext(context.Background(), query, args...)
}

func (pool *sqlPool) Prepare(query string) (*sql.Stmt, error) {
	return pool.PrepareContext(context.Background(), query)
}

func (pool *sqlPool) BeginTx(ctx context.Context, options *sql.TxOptions) (*sqlTransaction, error) {
	if pool.dialect == postgresDialect && options != nil && options.ReadOnly && options.Isolation == sql.LevelDefault {
		copy := *options
		copy.Isolation = sql.LevelRepeatableRead
		options = &copy
	}
	tx, err := pool.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlTransaction{Tx: tx, dialect: pool.dialect}, nil
}

func (pool *sqlPool) Begin() (*sqlTransaction, error) {
	return pool.BeginTx(context.Background(), nil)
}

type sqlTransaction struct {
	*sql.Tx
	dialect sqlDialect
}

func (tx *sqlTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, tx.dialect.bind(query), args...)
}

func (tx *sqlTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, tx.dialect.bind(query), args...)
}

func (tx *sqlTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, tx.dialect.bind(query), args...)
}

func (tx *sqlTransaction) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return tx.Tx.PrepareContext(ctx, tx.dialect.bind(query))
}

func (tx *sqlTransaction) Exec(query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(context.Background(), query, args...)
}

func (tx *sqlTransaction) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(context.Background(), query, args...)
}

func (tx *sqlTransaction) QueryRow(query string, args ...any) *sql.Row {
	return tx.QueryRowContext(context.Background(), query, args...)
}

func (tx *sqlTransaction) Prepare(query string) (*sql.Stmt, error) {
	return tx.PrepareContext(context.Background(), query)
}

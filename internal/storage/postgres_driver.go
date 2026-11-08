//go:build postgres

package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// Sanitize at the driver boundary, including deferred row and prepared-statement
// errors. PostgreSQL error details and pgx encoding errors can contain values or
// connection settings. Keep only stable control-flow errors and SQLSTATE; never
// retain the original cause for logging or later errors.As traversal.
func postgresDriverError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, io.EOF, driver.ErrBadConn, driver.ErrSkip, driver.ErrRemoveArgument, ErrDatabaseOwned, ErrDatabaseOwnershipLost} {
		if errors.Is(err, known) {
			return known
		}
	}
	var server *pgconn.PgError
	if errors.As(err, &server) {
		valid := len(server.Code) == 5
		for _, c := range server.Code {
			valid = valid && ((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z'))
		}
		if valid {
			return postgresStateError(server.Code)
		}
	}
	return errors.New("PostgreSQL operation failed")
}

type postgresStateError string

func (err postgresStateError) Error() string {
	return "PostgreSQL operation failed (SQLSTATE " + string(err) + ")"
}
func (err postgresStateError) SQLState() string { return string(err) }

func postgresDB(config pgx.ConnConfig, options ...stdlib.OptionOpenDB) *sql.DB {
	return sql.OpenDB(postgresConnector{stdlib.GetConnector(config, options...)})
}

type postgresConnector struct{ driver.Connector }

func (connector postgresConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := connector.Connector.Connect(ctx)
	if err != nil {
		return nil, postgresDriverError(err)
	}
	return postgresConn{conn.(*stdlib.Conn)}, nil
}

type postgresConn struct{ *stdlib.Conn }

func (conn postgresConn) Close() error { return postgresDriverError(conn.Conn.Close()) }
func (conn postgresConn) Ping(ctx context.Context) error {
	return postgresDriverError(conn.Conn.Ping(ctx))
}
func (conn postgresConn) ResetSession(ctx context.Context) error {
	return postgresDriverError(conn.Conn.ResetSession(ctx))
}
func (conn postgresConn) Prepare(query string) (driver.Stmt, error) {
	return conn.PrepareContext(context.Background(), query)
}
func (conn postgresConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := conn.Conn.PrepareContext(ctx, query)
	if err != nil {
		return nil, postgresDriverError(err)
	}
	return postgresStmt{stmt.(*stdlib.Stmt)}, nil
}
func (conn postgresConn) Begin() (driver.Tx, error) {
	return conn.BeginTx(context.Background(), driver.TxOptions{})
}
func (conn postgresConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	tx, err := conn.Conn.BeginTx(ctx, options)
	if err != nil {
		return nil, postgresDriverError(err)
	}
	return postgresTx{tx}, nil
}
func (conn postgresConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	result, err := conn.Conn.ExecContext(ctx, query, args)
	return result, postgresDriverError(err)
}
func (conn postgresConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := conn.Conn.QueryContext(ctx, query, args)
	return postgresDriverRows(rows, err)
}

type postgresTx struct{ driver.Tx }

func (tx postgresTx) Commit() error   { return postgresDriverError(tx.Tx.Commit()) }
func (tx postgresTx) Rollback() error { return postgresDriverError(tx.Tx.Rollback()) }

type postgresStmt struct{ *stdlib.Stmt }

func (stmt postgresStmt) Close() error { return postgresDriverError(stmt.Stmt.Close()) }
func (stmt postgresStmt) Exec(args []driver.Value) (driver.Result, error) {
	result, err := stmt.Stmt.Exec(args)
	return result, postgresDriverError(err)
}
func (stmt postgresStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	result, err := stmt.Stmt.ExecContext(ctx, args)
	return result, postgresDriverError(err)
}
func (stmt postgresStmt) Query(args []driver.Value) (driver.Rows, error) {
	rows, err := stmt.Stmt.Query(args)
	return postgresDriverRows(rows, err)
}
func (stmt postgresStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := stmt.Stmt.QueryContext(ctx, args)
	return postgresDriverRows(rows, err)
}

type postgresRows struct{ *stdlib.Rows }

func postgresDriverRows(rows driver.Rows, err error) (driver.Rows, error) {
	if err != nil {
		return nil, postgresDriverError(err)
	}
	return postgresRows{rows.(*stdlib.Rows)}, nil
}
func (rows postgresRows) Close() error { return postgresDriverError(rows.Rows.Close()) }
func (rows postgresRows) Next(values []driver.Value) error {
	return postgresDriverError(rows.Rows.Next(values))
}

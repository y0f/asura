//go:build postgres

package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed postgres_schema.sql
var postgresSchema string

const (
	postgresBaselineVersion = 40
	postgresLockNamespace   = 0x41535552 // ASUR, scoped to the connected database.
	postgresRuntimeLock     = 1
	postgresMigrationLock   = 2
)

var errPostgresDatabaseNotEmpty = errors.New("PostgreSQL initialization requires an empty dedicated database")

type postgresSchemaVersionError struct{ version int }

func (err postgresSchemaVersionError) Error() string {
	return fmt.Sprintf("unsupported PostgreSQL schema version %d (this build supports %d)", err.version, postgresBaselineVersion)
}

func init() { postgresOpener = openPostgres }

func openPostgres(ctx context.Context, options Options) (_ initializableStore, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(options.DSN)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection settings")
	}
	// Use a dedicated database and one fixed schema. A caller-controlled search
	// path must not redirect an unqualified table or function to another owner.
	config.RuntimeParams["search_path"] = "public"
	config.RuntimeParams["timezone"] = "UTC"
	config.RuntimeParams["application_name"] = "asura"
	if config.ConnectTimeout == 0 || config.ConnectTimeout > 10*time.Second {
		config.ConnectTimeout = 10 * time.Second
	}

	owner := &postgresOwnership{onLoss: options.OnOwnershipLoss}
	var writerOptions []stdlib.OptionOpenDB
	if options.RuntimeOwner {
		writerOptions = append(writerOptions,
			stdlib.OptionBeforeConnect(owner.beforeConnect),
			stdlib.OptionAfterConnect(owner.afterConnect))
	}
	writer := postgresDB(*config, writerOptions...)
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	// No idle/lifetime expiry: the sole writer session holds runtime ownership.
	readConfig := config.Copy()
	readConfig.RuntimeParams["default_transaction_read_only"] = "on"
	reader := postgresDB(*readConfig)
	reader.SetMaxOpenConns(options.MaxReadConns)
	reader.SetMaxIdleConns(options.MaxReadConns)
	store := &SQLStore{
		readDB:  &sqlPool{DB: reader, dialect: postgresDialect},
		writeDB: &sqlPool{DB: writer, dialect: postgresDialect},
	}
	defer func() {
		if err != nil {
			_ = store.Close()
		}
	}()
	if options.RuntimeOwner {
		store.writeDB.guard = owner.check
	}
	if err := writer.PingContext(ctx); err != nil {
		return nil, postgresStartupError("connect writer", err)
	}
	if err := reader.PingContext(ctx); err != nil {
		return nil, postgresStartupError("connect reader", err)
	}
	if err := migratePostgres(ctx, store.writeDB); err != nil {
		return nil, postgresStartupError("initialize schema", err)
	}
	if options.RuntimeOwner {
		observer := postgresDB(*readConfig)
		observer.SetMaxOpenConns(1)
		observer.SetMaxIdleConns(1)
		store.beforeClose = observer.Close
		if err := observer.PingContext(ctx); err != nil {
			return nil, postgresStartupError("connect ownership observer", err)
		}
		watchCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		store.beforeClose = func() error { cancel(); <-done; return observer.Close() }
		go func() {
			defer close(done)
			owner.watch(watchCtx, observer)
		}()
	}
	return store, nil
}

// Driver errors may contain connection settings or supplied values. Startup
// exposes stable context, cancellation and SQLSTATE, never the raw driver text.
func postgresStartupError(operation string, err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrDatabaseOwned, ErrDatabaseOwnershipLost, errPostgresDatabaseNotEmpty} {
		if errors.Is(err, known) {
			return fmt.Errorf("PostgreSQL %s: %w", operation, known)
		}
	}
	var schemaErr postgresSchemaVersionError
	if errors.As(err, &schemaErr) {
		return schemaErr
	}
	var server interface{ SQLState() string }
	if errors.As(err, &server) {
		return fmt.Errorf("PostgreSQL %s failed (SQLSTATE %s)", operation, server.SQLState())
	}
	return fmt.Errorf("PostgreSQL %s failed; check database availability and connection settings", operation)
}

func migratePostgres(ctx context.Context, db *sqlPool) error {
	if postgresBaselineVersion != schemaVersion {
		return postgresSchemaVersionError{version: schemaVersion}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?,?)", postgresLockNamespace, postgresMigrationLock); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT to_regclass('public.schema_version') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_class WHERE relnamespace='public'::regnamespace
			UNION ALL SELECT 1 FROM pg_proc WHERE pronamespace='public'::regnamespace
			UNION ALL SELECT 1 FROM pg_type WHERE typnamespace='public'::regnamespace
		)`).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return errPostgresDatabaseNotEmpty
		}
		if _, err := tx.ExecContext(ctx, postgresSchema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", postgresBaselineVersion); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key,value) VALUES ('tokens_hashed','1')"); err != nil {
			return err
		}
	} else {
		var version, count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MAX(version),0) FROM schema_version").Scan(&count, &version); err != nil {
			return err
		}
		if count != 1 || version != postgresBaselineVersion {
			return postgresSchemaVersionError{version: version}
		}
	}
	return tx.Commit()
}

type postgresOwnership struct {
	pid    atomic.Uint32
	lost   atomic.Bool
	once   sync.Once
	onLoss func()
}

func (owner *postgresOwnership) check() error {
	if owner.lost.Load() {
		return ErrDatabaseOwnershipLost
	}
	return nil
}

func (owner *postgresOwnership) lose() {
	owner.lost.Store(true)
	owner.once.Do(func() {
		if owner.onLoss != nil {
			owner.onLoss()
		}
	})
}

func (owner *postgresOwnership) beforeConnect(context.Context, *pgx.ConnConfig) error {
	if owner.pid.Load() != 0 {
		// A replaced writer is a runtime failure even if its old lock is free.
		// Another process may have taken ownership in between: require restart.
		owner.lose()
	}
	return owner.check()
}

func (owner *postgresOwnership) afterConnect(ctx context.Context, conn *pgx.Conn) (err error) {
	defer func() {
		if err != nil {
			// stdlib does not close a connection when its AfterConnect hook fails.
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = conn.Close(cleanup)
		}
	}()
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1,$2)", postgresLockNamespace, postgresRuntimeLock).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return ErrDatabaseOwned
	}
	owner.pid.Store(conn.PgConn().PID())
	return nil
}

func (owner *postgresOwnership) watch(ctx context.Context, reader *sql.DB) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			var held bool
			err := reader.QueryRowContext(queryCtx, `SELECT EXISTS(SELECT 1 FROM pg_locks
				WHERE locktype='advisory' AND pid=$1 AND classid=$2 AND objid=$3 AND objsubid=2 AND granted
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`,
				owner.pid.Load(), postgresLockNamespace, postgresRuntimeLock).Scan(&held)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil || !held {
				owner.lose()
				return
			}
		}
	}
}

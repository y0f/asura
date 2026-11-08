//go:build postgres

package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresDriverErrorSanitization(t *testing.T) {
	const secret = "private-fixture-value"
	for _, original := range []error{
		&pgconn.PgError{Code: "23503", Message: secret, Detail: secret, Where: secret, InternalQuery: secret},
		fmt.Errorf("encode argument %s", secret),
		fmt.Errorf("connect postgres://user:%s@host/database", secret),
	} {
		safe := postgresDriverError(original)
		if safe == nil || strings.Contains(fmt.Sprintf("%+v", safe), secret) || errors.Unwrap(safe) != nil {
			t.Fatalf("unsafe driver error: %v", safe)
		}
	}
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, driver.ErrBadConn, driver.ErrSkip, driver.ErrRemoveArgument, io.EOF, ErrDatabaseOwned, ErrDatabaseOwnershipLost} {
		if got := postgresDriverError(fmt.Errorf("%s: %w", secret, known)); got != known {
			t.Fatalf("lost control-flow sentinel %v", known)
		}
	}
	if err := monitorRelationError(postgresDriverError(&pgconn.PgError{Code: "23503", Message: secret})); !errors.Is(err, ErrInvalidMonitorRelation) {
		t.Fatalf("lost FK classification: %v", err)
	}
	if err := monitorRelationError(postgresDriverError(&pgconn.PgError{Code: "23505", Message: secret})); errors.Is(err, ErrInvalidMonitorRelation) {
		t.Fatal("unique violation misclassified as FK")
	}
}

func TestPostgresRuntimeErrorsDoNotExposeValues(t *testing.T) {
	s := postgresFixtureStore(t)
	ctx := context.Background()
	const secret = "private-fixture-parameter"
	assertSafe := func(err error) {
		t.Helper()
		if err == nil || strings.Contains(fmt.Sprintf("%+v", err), secret) {
			t.Fatalf("unsafe or missing runtime error: %v", err)
		}
		var raw *pgconn.PgError
		if errors.As(err, &raw) {
			t.Fatal("raw server error remains reachable")
		}
	}
	_, err := s.writeDB.ExecContext(ctx, "CREATE FUNCTION fixture_error(value text) RETURNS bigint LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '%', value; END; $$")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.writeDB.ExecContext(ctx, "SELECT fixture_error(?)", secret)
	assertSafe(err)
	var value int64
	assertSafe(s.readDB.QueryRowContext(ctx, "SELECT fixture_error(?)", secret).Scan(&value))
	stmt, err := s.writeDB.PrepareContext(ctx, "SELECT fixture_error(?)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx, secret)
	assertSafe(err)
	assertSafe(stmt.QueryRowContext(ctx, secret).Scan(&value))
	// pgx can include a value in its own encoding error, before server execution.
	_, err = s.writeDB.ExecContext(ctx, "SELECT ?::bigint", secret)
	assertSafe(err)
	// Exercise errors deferred until Rows.Next rather than initial Query.
	rows, err := s.readDB.QueryContext(ctx, "SELECT CASE WHEN n<20000 THEN n ELSE fixture_error(?) END FROM generate_series(1,20000) AS n", secret)
	if err != nil {
		assertSafe(err)
	} else {
		for rows.Next() {
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
		}
		assertSafe(rows.Err())
		rows.Close()
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.readDB.ExecContext(canceled, "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestPostgresUnprivilegedOwnerAndMaintenance(t *testing.T) {
	if os.Getenv("ASURA_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL fixture not configured")
	}
	config, err := pgx.ParseConfig(os.Getenv("ASURA_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal("invalid fixture config")
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	role := "asura_owner_" + strings.ReplaceAll(t.Name(), "TestPostgres", "")
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err := admin.Exec("CREATE ROLE " + quoted + " LOGIN PASSWORD 'fixture-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION"); err != nil {
		t.Fatal(err)
	}
	// Register before database cleanup so the role is removed after its database.
	t.Cleanup(func() {
		cleanup := stdlib.OpenDB(*config)
		defer cleanup.Close()
		if _, err := cleanup.Exec("DROP ROLE " + quoted); err != nil {
			t.Error(err)
		}
	})
	dsn := postgresFixtureDSN(t)
	u, _ := url.Parse(dsn)
	if _, err := admin.Exec("ALTER DATABASE " + pgx.Identifier{strings.TrimPrefix(u.Path, "/")}.Sanitize() + " OWNER TO " + quoted); err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, "fixture-only")
	store, err := Open(context.Background(), Options{Driver: "postgres", DSN: u.String(), MaxReadConns: 1, RuntimeOwner: true, EncryptionKey: "fixture-encryption-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	s := store.(*SQLStore)
	var privileged bool
	if err := s.readDB.QueryRow("SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication FROM pg_roles WHERE rolname=current_user").Scan(&privileged); err != nil || privileged {
		t.Fatalf("fixture unexpectedly privileged: %v", err)
	}
	var autovacuum string
	if err := s.readDB.QueryRow("SHOW autovacuum").Scan(&autovacuum); err != nil || autovacuum != "on" {
		t.Fatalf("autovacuum unavailable: %s %v", autovacuum, err)
	}
	m := newTestMonitor(t, s, "least privilege")
	if err := s.SetMonitorEnabled(context.Background(), m.ID, false); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetMonitor(context.Background(), m.ID)
	if err != nil || updated.ConfigRevision <= m.ConfigRevision {
		t.Fatalf("trigger failed for owner: %v", err)
	}
	if _, err := s.writeDB.Exec("VACUUM ANALYZE monitors"); err != nil {
		t.Fatal(err)
	}
	if size, err := s.DBSize(); err != nil || size <= 0 {
		t.Fatalf("database size failed: %d %v", size, err)
	}
}

func TestPostgresSchemaMatchesSQLite(t *testing.T) {
	pg := postgresFixtureStore(t)
	sq, err := NewSQLiteStore(filepath.Join(t.TempDir(), "schema.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer sq.Close()
	readStrings := func(db *sqlPool, query string) []string {
		t.Helper()
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var values []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(values)
		return values
	}
	compare := func(name, sqliteQuery, postgresQuery string) {
		t.Helper()
		want, got := readStrings(sq.readDB, sqliteQuery), readStrings(pg.readDB, postgresQuery)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s differ\nSQLite: %q\nPostgreSQL: %q", name, want, got)
		}
	}
	compare("tables", `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`, `SELECT tablename FROM pg_tables WHERE schemaname='public'`)
	compare("columns and affinities", `SELECT t.name||'.'||c.name||':'||upper(c.type) FROM sqlite_schema t JOIN pragma_table_info(t.name) c WHERE t.type='table' AND t.name NOT LIKE 'sqlite_%'`, `SELECT table_name||'.'||column_name||':'||CASE data_type WHEN 'bigint' THEN 'INTEGER' WHEN 'double precision' THEN 'REAL' ELSE upper(data_type) END FROM information_schema.columns WHERE table_schema='public'`)
	compare("primary key columns", `SELECT t.name||':'||c.pk||':'||c.name FROM sqlite_schema t JOIN pragma_table_info(t.name) c WHERE t.type='table' AND c.pk>0`, `SELECT t.relname||':'||k.ordinality||':'||a.attname FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace CROSS JOIN LATERAL unnest(c.conkey) WITH ORDINALITY k(attnum,ordinality) JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=k.attnum WHERE c.contype='p' AND n.nspname='public'`)
	compare("named index columns", `SELECT s.tbl_name||'.'||s.name||':'||c.seqno||':'||c.name FROM sqlite_schema s JOIN pragma_index_info(s.name) c WHERE s.type='index' AND s.name LIKE 'idx_%'`, `SELECT t.relname||'.'||i.relname||':'||(k.ordinality-1)||':'||a.attname FROM pg_index x JOIN pg_class i ON i.oid=x.indexrelid JOIN pg_class t ON t.oid=x.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace CROSS JOIN LATERAL unnest(x.indkey) WITH ORDINALITY k(attnum,ordinality) JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=k.attnum WHERE n.nspname='public' AND i.relname LIKE 'idx_%'`)
	compare("foreign keys", `SELECT s.name||'.'||f."from"||'->'||f."table"||'.'||f."to"||':'||f.on_delete FROM sqlite_schema s JOIN pragma_foreign_key_list(s.name) f WHERE s.type='table'`, `SELECT t.relname||'.'||a.attname||'->'||rt.relname||'.'||ra.attname||':'||CASE c.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace JOIN pg_class rt ON rt.oid=c.confrelid CROSS JOIN LATERAL unnest(c.conkey,c.confkey) k(local,remote) JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=k.local JOIN pg_attribute ra ON ra.attrelid=rt.oid AND ra.attnum=k.remote WHERE c.contype='f' AND n.nspname='public'`)
}

func TestPostgresInitializationFailureRollsBackAndReleasesOwnership(t *testing.T) {
	dsn := postgresFixtureDSN(t)
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid fixture config")
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	// Fail after the complete baseline, proving transactional DDL rollback.
	// This nonparallel test changes only its process-local embedded statement.
	t.Setenv("ASURA_STORAGE_SCHEMA_FAILURE", "1")
	baseline := postgresSchema
	defer func() { postgresSchema = baseline }()
	postgresSchema += ` DO $$ BEGIN RAISE EXCEPTION 'injected final baseline failure'; END; $$;`
	options := Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1, RuntimeOwner: true}
	if s, err := Open(context.Background(), options); err == nil || s != nil {
		if s != nil {
			s.Close()
		}
		t.Fatalf("failed schema initialization succeeded: %v", err)
	}
	var tables int
	if err := admin.QueryRow("SELECT count(*) FROM pg_tables WHERE schemaname='public'").Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("initialization partially committed: %d %v", tables, err)
	}
	assertConnections := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var got int
			if err := admin.QueryRow("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name='asura'").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("leaked connections: got%d want%d", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	assertConnections(0)
	postgresSchema = baseline
	s, err := Open(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertConnections(3) // writer, ordinary reader, independent ownership observer
	for range 3 {
		if other, err := Open(context.Background(), options); !errors.Is(err, ErrDatabaseOwned) || other != nil {
			if other != nil {
				other.Close()
			}
			t.Fatalf("competing owner accepted: %v", err)
		}
	}
	assertConnections(3)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertConnections(0)
}

func TestPostgresInitializationPreservesExistingSchemaObjects(t *testing.T) {
	for _, object := range []struct{ name, create, present string }{
		{"view", "CREATE VIEW unrelated_fixture AS SELECT 1 AS value", "SELECT to_regclass('public.unrelated_fixture') IS NOT NULL"},
		{"function", "CREATE FUNCTION unrelated_fixture() RETURNS int LANGUAGE SQL AS 'SELECT 1'", "SELECT to_regprocedure('public.unrelated_fixture()') IS NOT NULL"},
		{"type", "CREATE TYPE unrelated_fixture AS ENUM ('fixture')", "SELECT to_regtype('public.unrelated_fixture') IS NOT NULL"},
	} {
		t.Run(object.name, func(t *testing.T) {
			dsn := postgresFixtureDSN(t)
			config, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatal("invalid fixture config")
			}
			admin := stdlib.OpenDB(*config)
			defer admin.Close()
			if _, err := admin.Exec(object.create); err != nil {
				t.Fatal(err)
			}
			if s, err := Open(context.Background(), Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1}); !errors.Is(err, errPostgresDatabaseNotEmpty) || s != nil {
				if s != nil {
					s.Close()
				}
				t.Fatalf("existing object admitted initialization: %v", err)
			}
			var present bool
			if err := admin.QueryRow(object.present).Scan(&present); err != nil || !present {
				t.Fatalf("existing object changed: %v", err)
			}
			if err := admin.QueryRow("SELECT to_regclass('public.schema_version') IS NOT NULL").Scan(&present); err != nil || present {
				t.Fatalf("partial schema created: %v", err)
			}
		})
	}
}

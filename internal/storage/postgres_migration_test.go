//go:build postgres

package storage

import (
	"context"
	_ "embed"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Exact original PostgreSQL schema from commit9943201, not reconstructed from
// current DDL. Test-only embedding does not increase the production binary.
//
//go:embed testdata/postgres-v40.sql
var postgresSchema40 string

func postgres40Fixture(t *testing.T) (*sqlPool, string) {
	t.Helper()
	dsn := postgresFixtureDSN(t)
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid fixture DSN")
	}
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { admin.Close() })
	role := config.Database + "_owner"
	quotedRole := pgx.Identifier{role}.Sanitize()
	if _, err := admin.Exec("CREATE ROLE " + quotedRole + " LOGIN PASSWORD 'fixture-only-owner'"); err != nil {
		t.Fatal(postgresStartupError("fixture owner", err))
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP OWNED BY " + quotedRole + " CASCADE"); err != nil {
			t.Error(postgresStartupError("fixture cleanup", err))
		}
		if _, err := admin.Exec("DROP ROLE " + quotedRole); err != nil {
			t.Error(postgresStartupError("fixture owner cleanup", err))
		}
	})
	if _, err := admin.Exec("GRANT USAGE, CREATE ON SCHEMA public TO " + quotedRole); err != nil {
		t.Fatal(postgresStartupError("fixture grant", err))
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, "fixture-only-owner")
	dsn = u.String()
	config.User, config.Password = role, "fixture-only-owner"
	db := &sqlPool{DB: postgresDB(*config), dialect: postgresDialect}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(postgresSchema40); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES(40);
		INSERT INTO meta(key,value) VALUES('tokens_hashed','1');
		INSERT INTO monitors(name,type,target) VALUES('legacy','http','https://example.invalid');
		INSERT INTO monitor_status(monitor_id,status,last_check_at,last_response_time,baseline_avg,baseline_stddev,baseline_updated_at) VALUES(1,'up','2026-01-02T03:00:00Z',7,9,3,'2026-01-02T03:00:00Z');
		INSERT INTO check_results(monitor_id,status,response_time,message,created_at) VALUES(1,'up',7,'legacy-history','2026-01-02T03:00:00Z');
		INSERT INTO check_result_hourly(monitor_id,hour,total,up_count,down_count,avg_rt) VALUES(1,'2026-01-02T03',5,4,1,7);
		INSERT INTO check_result_daily(monitor_id,day,total,up_count,down_count,avg_rt) VALUES(1,'2026-01-02',5,4,1,7)`); err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

func TestPostgresObservationUpgradePreservesHistoryAndConstraints(t *testing.T) {
	db, dsn := postgres40Fixture(t)
	store, err := Open(context.Background(), Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1, RuntimeOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 41 {
		t.Fatalf("version: %d %v", version, err)
	}
	var privileged bool
	if err := db.QueryRow("SELECT rolsuper OR rolcreatedb OR rolcreaterole FROM pg_roles WHERE rolname=current_user").Scan(&privileged); err != nil || privileged {
		t.Fatalf("fixture privilege: %v %v", privileged, err)
	}
	for _, table := range []string{"check_results", "monitor_status", "check_result_hourly", "check_result_daily"} {
		var kind string
		if err := db.QueryRow("SELECT observation_kind FROM " + table).Scan(&kind); err != nil || kind != "unknown" {
			t.Fatalf("%s inferred legacy kind: %q %v", table, kind, err)
		}
		if _, err := db.Exec("UPDATE " + table + " SET observation_kind='invalid'"); err == nil {
			t.Fatalf("%s accepted invalid kind", table)
		}
		if _, err := db.Exec("UPDATE " + table + " SET observation_kind=NULL"); err == nil {
			t.Fatalf("%s accepted missing kind", table)
		}
	}
	var message string
	var response, total, up int64
	if err := db.QueryRow("SELECT message,response_time FROM check_results").Scan(&message, &response); err != nil || message != "legacy-history" || response != 7 {
		t.Fatalf("raw history changed: %q %d %v", message, response, err)
	}
	var preserved bool
	if err := db.QueryRow(`SELECT baseline_avg=0 AND baseline_stddev=0 AND baseline_updated_at IS NULL
		AND status='up' AND last_response_time=7 AND last_check_at='2026-01-02T03:00:00Z' FROM monitor_status`).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("baseline invalidation changed recorded state: %v %v", preserved, err)
	}
	var nextID int64
	if err := db.QueryRow("INSERT INTO monitors(name,type,target) VALUES('next','http','https://example.invalid') RETURNING id").Scan(&nextID); err != nil || nextID != 2 {
		t.Fatalf("monitor identity lost: %d %v", nextID, err)
	}
	if err := db.QueryRow("INSERT INTO check_results(monitor_id,status) VALUES(1,'up') RETURNING id").Scan(&nextID); err != nil || nextID != 2 {
		t.Fatalf("observation identity lost: %d %v", nextID, err)
	}
	if _, err := db.Exec("DELETE FROM check_results WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ table, bucket, value string }{{"check_result_hourly", "hour", "2026-01-02T03"}, {"check_result_daily", "day", "2026-01-02"}} {
		if err := db.QueryRow("SELECT total,up_count FROM "+row.table).Scan(&total, &up); err != nil || total != 5 || up != 4 {
			t.Fatalf("rollup changed: %d %d %v", total, up, err)
		}
		query := "INSERT INTO " + row.table + " (monitor_id," + row.bucket + ",observation_kind,total) VALUES(1,?,?,1)"
		for _, kind := range []string{"probe", "manual", "heartbeat"} {
			if _, err := db.Exec(query, row.value, kind); err != nil {
				t.Fatalf("distinct kind conflicts: %v", err)
			}
		}
		if _, err := db.Exec(query, row.value, "probe"); err == nil {
			t.Fatal("duplicate rollup accepted")
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1, RuntimeOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := db.Exec("DELETE FROM monitors WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"check_results", "monitor_status", "check_result_hourly", "check_result_daily"} {
		var rows int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("%s lost cascading FK: %d %v", table, rows, err)
		}
	}
}

func TestPostgresObservationUpgradeRollsBackAndCanRetry(t *testing.T) {
	db, dsn := postgres40Fixture(t)
	t.Setenv("ASURA_STORAGE_UPGRADE_FAILURE", "1") // Prevent concurrent/parallel mutation.
	original := postgresMigration41
	defer func() { postgresMigration41 = original }()
	postgresMigration41 += ` DO $$ BEGIN RAISE EXCEPTION 'injected-private-upgrade-failure'; END; $$;`
	options := Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1, RuntimeOwner: true}
	if store, err := Open(context.Background(), options); err == nil || store != nil || strings.Contains(err.Error(), "injected-private") {
		t.Fatalf("failed upgrade exposed/returned success: %v", err)
	}
	assertPostgres40Unchanged(t, db)
	postgresMigration41 = original
	store, err := Open(context.Background(), options)
	if err != nil {
		t.Fatalf("retry/ownership release: %v", err)
	}
	defer store.Close()
}

func assertPostgres40Unchanged(t *testing.T, db *sqlPool) {
	t.Helper()
	var version, columns, rows int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 40 {
		t.Fatalf("failed version advanced: %d %v", version, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND column_name='observation_kind'").Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("partial DDL: %d %v", columns, err)
	}
	for _, table := range []string{"check_results", "monitor_status", "check_result_hourly", "check_result_daily"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("%s history changed: %d %v", table, rows, err)
		}
	}
	if _, err := db.Exec("INSERT INTO check_result_daily(monitor_id,day,total) VALUES(1,'2026-01-02',1)"); err == nil {
		t.Fatal("old primary key was not restored")
	}
	var baselinePreserved bool
	if err := db.QueryRow("SELECT baseline_avg=9 AND baseline_stddev=3 AND baseline_updated_at IS NOT NULL FROM monitor_status").Scan(&baselinePreserved); err != nil || !baselinePreserved {
		t.Fatalf("failed upgrade cleared baseline: %v %v", baselinePreserved, err)
	}
}

func TestPostgresObservationVersionFailureRollsBack(t *testing.T) {
	db, _ := postgres40Fixture(t)
	if _, err := db.Exec(`CREATE FUNCTION fixture_version_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'fixture version update rejected'; END; $$;
		CREATE TRIGGER fixture_version_failure BEFORE UPDATE ON schema_version
		FOR EACH ROW EXECUTE FUNCTION fixture_version_failure()`); err != nil {
		t.Fatal(err)
	}
	if err := migratePostgres(context.Background(), db); err == nil {
		t.Fatal("version failure reported successful upgrade")
	}
	assertPostgres40Unchanged(t, db)
	if _, err := db.Exec("DROP FUNCTION fixture_version_failure() CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err := migratePostgres(context.Background(), db); err != nil {
		t.Fatalf("retry after version failure: %v", err)
	}
}

func TestPostgresObservationUpgradeCannotRunUnderAnotherRuntime(t *testing.T) {
	db, dsn := postgres40Fixture(t)
	ctx := context.Background()
	owner, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.ExecContext(ctx, "SELECT pg_advisory_lock($1,$2)", postgresLockNamespace, postgresRuntimeLock); err != nil {
		t.Fatal(err)
	}
	// A maintenance CLI skips the normal runtime session lease. Its migration
	// must still reject an older live runtime before any DDL is applied.
	if store, err := Open(ctx, Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1}); !errors.Is(err, ErrDatabaseOwned) || store != nil {
		t.Fatalf("CLI upgraded live runtime: %v", err)
	}
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 40 {
		t.Fatalf("live runtime schema changed: %d %v", version, err)
	}
	if _, err := owner.ExecContext(ctx, "SELECT pg_advisory_unlock($1,$2)", postgresLockNamespace, postgresRuntimeLock); err != nil {
		t.Fatal(err)
	}
	if err := migratePostgres(ctx, db); err != nil {
		t.Fatalf("upgrade after runtime exit: %v", err)
	}
}

func TestPostgresObservationConcurrentUpgradesSerialize(t *testing.T) {
	db, _ := postgres40Fixture(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var done sync.WaitGroup
	for range 2 {
		done.Go(func() { <-start; errs <- migratePostgres(context.Background(), db) })
	}
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 41 {
		t.Fatalf("concurrent version: %d %v", version, err)
	}
}

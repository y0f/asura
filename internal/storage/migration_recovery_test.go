package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshSchemaFailureCanRetry(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA max_page_count=2"); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err == nil {
		t.Fatal("expected initialization to exceed the disk page limit")
	}
	if _, err := db.Exec("PRAGMA max_page_count=10000"); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("retry after restoring capacity: %v", err)
	}
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema version = %d, %v; want %d", version, err, schemaVersion)
	}
}

func TestMigrationsRejectFutureSchema(t *testing.T) {
	s := testStore(t)
	if _, err := s.writeDB.Exec("UPDATE schema_version SET version=?", schemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(s.writeDB.DB); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("expected actionable error for a newer schema: %v", err)
	}
	var version int
	if err := s.readDB.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != schemaVersion+1 {
		t.Fatalf("future schema must remain unchanged: version=%d, err=%v", version, err)
	}
}

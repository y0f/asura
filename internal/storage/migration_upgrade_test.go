package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reopenAtVersion rolls a fresh database back to schema version v (after
// running downgrade, which undoes what later migrations created) and reopens
// it, so the migrations after v run as they do on upgrade.
func reopenAtVersion(t *testing.T, v int, downgrade func(s *SQLiteStore)) *SQLiteStore {
	t.Helper()
	f, err := os.CreateTemp("", "asura-upgrade-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	s, err := NewSQLiteStore(f.Name(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if v < 36 {
		for _, table := range []string{"check_result_hourly", "check_result_daily"} {
			if _, err := s.writeDB.Exec("ALTER TABLE " + table + " DROP COLUMN sum_rt"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if v < 37 {
		if _, err := s.writeDB.Exec("DROP INDEX idx_incidents_pending"); err != nil {
			t.Fatal(err)
		}
		for _, column := range []string{"announcement_pending", "escalation_pending"} {
			if _, err := s.writeDB.Exec("ALTER TABLE incidents DROP COLUMN " + column); err != nil {
				t.Fatal(err)
			}
		}
	}
	if downgrade != nil {
		downgrade(s)
	}
	if _, err := s.writeDB.Exec("UPDATE schema_version SET version = ?", v); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = NewSQLiteStore(f.Name(), 1)
	if err != nil {
		t.Fatalf("reopen at v%d: %v", v, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A v34 database (before agent assignments) gets the monitor_agents table.
func TestUpgradeFromV34AddsMonitorAgents(t *testing.T) {
	ctx := context.Background()
	s := reopenAtVersion(t, 34, func(s *SQLiteStore) {
		if _, err := s.writeDB.Exec("DROP TABLE monitor_agents"); err != nil {
			t.Fatal(err)
		}
	})
	a := &Agent{Name: "probe", Enabled: true}
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{Name: "m", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10,
		Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1}
	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID}); err != nil {
		t.Fatalf("set agents after upgrade: %v", err)
	}
	if ids, err := s.GetMonitorAgentIDs(ctx, m.ID); err != nil || len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("agents after upgrade = %v, %v; want [%d]", ids, err, a.ID)
	}
}

func TestUpgradeFromV35PreservesRollupSamples(t *testing.T) {
	var monitorID int64
	s := reopenAtVersion(t, 35, func(s *SQLiteStore) {
		monitorID = newTestMonitor(t, s, "legacy rollup").ID
		if _, err := s.writeDB.Exec(`INSERT INTO check_result_hourly (monitor_id, hour, avg_rt, up_count, total)
			VALUES (?, '2026-01-02T00', 100, 9, 9), (?, '2026-01-02T01', 1000, 1, 1)`, monitorID, monitorID); err != nil {
			t.Fatal(err)
		}
	})
	var knownSums int
	if err := s.readDB.QueryRow(`SELECT COUNT(sum_rt) FROM check_result_hourly WHERE monitor_id=?`, monitorID).Scan(&knownSums); err != nil {
		t.Fatal(err)
	}
	if knownSums != 0 {
		t.Fatal("migration must not invent exact response-time sums for historical rounded means")
	}
	if err := s.RollupDaily(context.Background(), "2026-01-02"); err != nil {
		t.Fatal(err)
	}
	var mean, total int64
	if err := s.readDB.QueryRow(`SELECT avg_rt, total FROM check_result_daily WHERE monitor_id=?`, monitorID).Scan(&mean, &total); err != nil {
		t.Fatal(err)
	}
	if mean != 190 || total != 10 {
		t.Errorf("upgraded rollup mean/count = %d/%d, want 190/10", mean, total)
	}
}

// openV130 creates a database exactly as Asura v1.3.0 did, from its base
// schema (testdata/schema-v1.3.0.sql) stamped as schema version 33, and opens
// it with the current code, which runs the migrations after v33.
func openV130(t *testing.T) *SQLiteStore {
	t.Helper()
	schemaV130, err := os.ReadFile(filepath.Join("testdata", "schema-v1.3.0.sql"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "asura-v130-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := sql.Open("sqlite", "file:"+f.Name()+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schemaV130)); err != nil {
		db.Close()
		t.Fatalf("apply v1.3.0 schema: %v", err)
	}
	if _, err := db.Exec("INSERT INTO schema_version (version) VALUES (33)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewSQLiteStore(f.Name(), 1)
	if err != nil {
		t.Fatalf("open v1.3.0 database: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// schemaVersion is the last migration, and a database created by v1.3.0 that
// runs the migrations ends up with the same tables, columns and indexes as a
// fresh database built from schema.
func TestUpgradeFromV130MatchesFreshSchema(t *testing.T) {
	if last := migrations[len(migrations)-1].version; last != schemaVersion {
		t.Fatalf("schemaVersion = %d, last migration = %d", schemaVersion, last)
	}
	fresh := reopenAtVersion(t, schemaVersion, nil)
	upgraded := openV130(t)
	want, got := schemaShape(t, fresh), schemaShape(t, upgraded)
	for k := range want {
		if !got[k] {
			t.Errorf("upgraded database lacks %s", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("upgraded database has extra %s", k)
		}
	}
	var v int
	if err := upgraded.readDB.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("upgraded schema version = %d, %v; want %d", v, err, schemaVersion)
	}
}

// schemaShape lists every table, every table column with its type,
// constraints and default, and every index with its table, columns and
// uniqueness.
func schemaShape(t *testing.T, s *SQLiteStore) map[string]bool {
	t.Helper()
	rows, err := s.readDB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()

	shape := map[string]bool{}
	query := func(q string, args ...any) *sql.Rows {
		t.Helper()
		rows, err := s.readDB.Query(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	for _, tbl := range tables {
		shape["table "+tbl] = true
		cols := query(`SELECT name, type, "notnull", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info(?)`, tbl)
		for cols.Next() {
			var name, typ, dflt string
			var notNull, pk int
			if err := cols.Scan(&name, &typ, &notNull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			shape[fmt.Sprintf("column %s.%s %s notnull=%d default=%s pk=%d", tbl, name, typ, notNull, dflt, pk)] = true
		}
		cols.Close()

		idxs := query(`SELECT name, "unique", partial FROM pragma_index_list(?)`, tbl)
		type index struct {
			name            string
			unique, partial int
		}
		var indexes []index
		for idxs.Next() {
			var ix index
			if err := idxs.Scan(&ix.name, &ix.unique, &ix.partial); err != nil {
				t.Fatal(err)
			}
			indexes = append(indexes, ix)
		}
		idxs.Close()
		for _, ix := range indexes {
			var keys []string
			info := query(`SELECT COALESCE(name, '<expr>'), "desc" FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno`, ix.name)
			for info.Next() {
				var col string
				var desc int
				if err := info.Scan(&col, &desc); err != nil {
					t.Fatal(err)
				}
				if desc == 1 {
					col += " DESC"
				}
				keys = append(keys, col)
			}
			info.Close()
			shape[fmt.Sprintf("index %s on %s(%s) unique=%d partial=%d", ix.name, tbl, strings.Join(keys, ", "), ix.unique, ix.partial)] = true
		}
	}
	return shape
}

// Upgrading a v1.3.0 database leaves status page settings as they were: the
// public JSON gating did not change, so no migration may rewrite api_enabled.
func TestUpgradeFromV130KeepsStatusPageSettings(t *testing.T) {
	ctx := context.Background()
	var pages []*StatusPage
	s := reopenAtVersion(t, 33, func(s *SQLiteStore) {
		pages = []*StatusPage{
			{Slug: "public", Title: "Public", Enabled: true},
			{Slug: "off", Title: "Off"},
			{Slug: "api-only", Title: "API only", APIEnabled: true},
			{Slug: "both", Title: "Both", Enabled: true, APIEnabled: true},
		}
		for _, sp := range pages {
			if err := s.CreateStatusPage(ctx, sp); err != nil {
				t.Fatal(err)
			}
		}
	})
	for _, want := range pages {
		got, err := s.GetStatusPage(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled != want.Enabled || got.APIEnabled != want.APIEnabled {
			t.Errorf("page %q after upgrade: enabled=%v api_enabled=%v, want enabled=%v api_enabled=%v",
				got.Slug, got.Enabled, got.APIEnabled, want.Enabled, want.APIEnabled)
		}
	}
}

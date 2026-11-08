package storage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Preserve real table constraints and whichever earlier columns the requested
// legacy fixture still has, while removing only the v41 provenance additions.
func downgradeObservationKinds(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, table := range []string{"check_result_hourly", "check_result_daily"} {
		var ddl string
		if err := s.writeDB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(ddl, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "observation_kind ") {
				kept = append(kept, strings.ReplaceAll(line, ", observation_kind", ""))
			}
		}
		ddl = strings.Replace(strings.Join(kept, "\n"), table, table+"_v40", 1)
		rows, err := s.writeDB.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for rows.Next() {
			var id, notNull, primary int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&id, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if name != "observation_kind" {
				columns = append(columns, name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		list := strings.Join(columns, ",")
		for _, query := range []string{ddl, "INSERT INTO " + table + "_v40 (" + list + ") SELECT " + list + " FROM " + table, "DROP TABLE " + table, "ALTER TABLE " + table + "_v40 RENAME TO " + table} {
			if _, err := s.writeDB.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, table := range []string{"check_results", "monitor_status"} {
		if _, err := s.writeDB.Exec("ALTER TABLE " + table + " DROP COLUMN observation_kind"); err != nil {
			t.Fatal(err)
		}
	}
}

func observationV40Fixture(t *testing.T) (string, time.Time) {
	t.Helper()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	path := filepath.Join(t.TempDir(), "historical.db")
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), OmitHost: true}).String()
	db, err := sql.Open("sqlite", uri+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	historical, err := os.ReadFile(filepath.Join("testdata", "schema-v40.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(historical)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO monitors(id,name,type,target) VALUES(1,'legacy','http','https://example.com')`, nil},
		{`INSERT INTO check_results(monitor_id,status,response_time,message,created_at) VALUES(1,'up',25,'legacy message',?)`, []any{formatTime(day.Add(time.Hour))}},
		{`INSERT INTO check_result_hourly(monitor_id,hour,avg_rt,up_count,total,sum_rt) VALUES(1,?,25,1,1,NULL)`, []any{day.Add(time.Hour).Format("2006-01-02T15")}},
		{`INSERT INTO check_result_daily(monitor_id,day,avg_rt,up_count,total,sum_rt) VALUES(1,?,25,1,1,25)`, []any{day.Format("2006-01-02")}},
		{`INSERT INTO monitor_status(monitor_id,status,baseline_avg,baseline_stddev,baseline_updated_at) VALUES(1,'up',25,1,?)`, []any{formatTime(day)}},
	} {
		if _, err := db.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path, day
}

func TestObservationMigrationPreservesUnclassifiedHistory(t *testing.T) {
	ctx := context.Background()
	const monitorID int64 = 1
	path, day := observationV40Fixture(t)
	s, err := NewSQLiteStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, table := range []string{"check_results", "check_result_hourly", "check_result_daily"} {
		var count int
		if err := s.readDB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE observation_kind='unknown' AND monitor_id=?", monitorID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("legacy %s changed or guessed: count%d %v", table, count, err)
		}
	}
	rows, err := s.ListCheckResults(ctx, monitorID, Pagination{Page: 1, PerPage: 10})
	if err != nil || rows.Data.([]*CheckResult)[0].Message != "legacy message" {
		t.Fatalf("legacy detail lost: %v", err)
	}
	status, err := s.GetMonitorStatus(ctx, monitorID)
	if err != nil || status.ObservationKind != ObservationUnknown || status.BaselineUpdatedAt != nil || status.BaselineAvg != 0 {
		t.Fatalf("unproven baseline survived: %+v %v", status, err)
	}
	days, err := s.GetDailyUptime(ctx, monitorID, day, day.AddDate(0, 0, 1))
	_, samples := SummarizeUptime(days)
	if err != nil || samples != 0 {
		t.Fatalf("legacy rows became verified samples: %d %v", samples, err)
	}
	if _, err := s.writeDB.Exec("UPDATE check_results SET observation_kind='forged'"); err == nil {
		t.Fatal("unknown provenance enum accepted")
	}
}

func TestObservationMigrationLateFailureRollsBackAndRetries(t *testing.T) {
	path, _ := observationV40Fixture(t)
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), OmitHost: true}).String()
	db, err := sql.Open("sqlite", uri+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Abort the final version write, after both destructive table rebuilds and
	// cache invalidation have executed. Every preceding DDL/DML must roll back.
	if _, err := db.Exec(`CREATE TRIGGER reject_observation_migration
		BEFORE UPDATE ON schema_version WHEN NEW.version=41
		BEGIN SELECT RAISE(ABORT, 'fixture migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if s, err := NewSQLiteStore(path, 1); err == nil {
		s.Close()
		t.Fatal("late migration failure unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "fixture migration failure") {
		t.Fatalf("migration failed at unintended boundary: %v", err)
	}
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 40 {
		t.Fatalf("failed migration changed version: %d %v", version, err)
	}
	for _, table := range []string{"check_results", "monitor_status", "check_result_hourly", "check_result_daily"} {
		var count, kinds int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("rollback lost %s rows: %d %v", table, count, err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='observation_kind'", table).Scan(&kinds); err != nil || kinds != 0 {
			t.Fatalf("rollback retained %s provenance column: %d %v", table, kinds, err)
		}
		if table == "check_result_hourly" || table == "check_result_daily" {
			var columns int
			if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE pk>0", table).Scan(&columns); err != nil || columns != 2 {
				t.Fatalf("rollback changed %s primary key: %d %v", table, columns, err)
			}
		}
	}
	var avg, stddev float64
	var updated string
	if err := db.QueryRow("SELECT baseline_avg,baseline_stddev,baseline_updated_at FROM monitor_status WHERE monitor_id=1").Scan(&avg, &stddev, &updated); err != nil || avg != 25 || stddev != 1 || updated == "" {
		t.Fatalf("rollback lost baseline: %v/%v/%q %v", avg, stddev, updated, err)
	}
	var message string
	var unknownSum sql.NullInt64
	if err := db.QueryRow("SELECT message FROM check_results WHERE monitor_id=1").Scan(&message); err != nil || message != "legacy message" {
		t.Fatalf("rollback changed raw history: %q %v", message, err)
	}
	if err := db.QueryRow("SELECT sum_rt FROM check_result_hourly WHERE monitor_id=1").Scan(&unknownSum); err != nil || unknownSum.Valid {
		t.Fatalf("rollback fabricated an exact legacy sum: %+v %v", unknownSum, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_observation_migration"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path, 1)
	if err != nil {
		t.Fatalf("migration retry failed: %v", err)
	}
	defer s.Close()
	for _, table := range []string{"check_results", "monitor_status", "check_result_hourly", "check_result_daily"} {
		var count int
		if err := s.readDB.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE observation_kind='unknown'").Scan(&count); err != nil || count != 1 {
			t.Fatalf("retry changed preserved %s rows: %d %v", table, count, err)
		}
	}
	status, err := s.GetMonitorStatus(context.Background(), 1)
	if err != nil || status.BaselineAvg != 0 || status.BaselineStddev != 0 || status.BaselineUpdatedAt != nil {
		t.Fatalf("successful retry retained unproven baseline: %+v %v", status, err)
	}
}

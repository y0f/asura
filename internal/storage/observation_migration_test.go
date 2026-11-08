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

func TestObservationMigrationPreservesUnclassifiedHistory(t *testing.T) {
	ctx := context.Background()
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	const monitorID int64 = 1
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

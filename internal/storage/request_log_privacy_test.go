package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func legacyRequestLog(t *testing.T, store *SQLStore, path, referer string) {
	t.Helper()
	_, err := store.writeDB.Exec(`INSERT INTO request_logs
		(method,path,status_code,latency_ms,client_ip,referer,route_group,created_at)
		VALUES ('GET',?,200,7,'fixture-client',?,'api','2026-11-08T12:00:00Z')`, path, referer)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRequestLogPrivacyInsertionAndReadDefense(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "/asura/api/v1/heartbeat/private-credential"
	const referer = "https://user:password@example.test/status/confirm?token=private-credential#private-fragment"
	entry := &RequestLog{Method: "GET", Path: path, Referer: referer, StatusCode: 200, CreatedAt: time.Now()}
	if err := s.InsertRequestLogBatch(ctx, []*RequestLog{entry}); err != nil {
		t.Fatal(err)
	}
	var storedPath, storedReferer string
	if err := s.readDB.QueryRow("SELECT path, referer FROM request_logs").Scan(&storedPath, &storedReferer); err != nil {
		t.Fatal(err)
	}
	if storedPath != "/asura/api/v1/heartbeat/[redacted]" || storedReferer != "https://example.test/status/confirm" {
		t.Fatalf("insertion retained private metadata: path=%q referer=%q", storedPath, storedReferer)
	}
	if entry.Path != path || entry.Referer != referer {
		t.Fatal("storage mutated caller-owned request metadata")
	}
	// A defense on reads protects even callers which open an old database
	// directly instead of using production Open's startup finalization.
	legacyRequestLog(t, s, path, referer)
	result, err := s.ListRequestLogs(ctx, RequestLogFilter{}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range result.Data.([]*RequestLog) {
		if got.Path != storedPath || got.Referer != storedReferer {
			t.Fatalf("read returned unsanitized metadata: %+v", got)
		}
	}
	stats, err := s.GetRequestLogStats(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range [][]PathCount{stats.TopPaths, stats.TopReferers} {
		for _, value := range group {
			if strings.Contains(value.Path, "private-") || strings.Contains(value.Path, "password") {
				t.Fatalf("aggregate returned a credential: %+v", value)
			}
		}
	}
}

func TestRequestLogPrivacyCleanupPreservesAccounting(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.writeDB.Exec("DELETE FROM meta WHERE key=?", requestLogPrivacyKey); err != nil {
		t.Fatal(err)
	}
	// More than one keyset batch, and more unique secret paths than the top-10
	// output limit: canonicalization must happen before grouping and LIMIT.
	for i := range 260 {
		legacyRequestLog(t, s, fmt.Sprintf("/asura/api/v1/heartbeat/secret-%d", i), fmt.Sprintf("https://example.test/status/unsubscribe?token=secret-%d", i))
	}
	for range 3 {
		legacyRequestLog(t, s, "/monitors", "https://example.test/monitors")
	}
	if err := s.RollupRequestLogs(ctx, "2026-11-08"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequestLogPrivacy(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := s.GetRequestLogStats(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRequests != 263 || stats.UniqueVisitors != 1 || stats.AvgLatencyMs != 7 || len(stats.TopPaths) != 2 || stats.TopPaths[0] != (PathCount{Path: "/asura/api/v1/heartbeat/[redacted]", Count: 260}) || len(stats.TopReferers) != 2 || stats.TopReferers[0] != (PathCount{Path: "https://example.test/status/unsubscribe", Count: 260}) {
		t.Fatalf("cleanup lost or misgrouped request accounting: %+v", stats)
	}
	var rollupCount int64
	if err := s.readDB.QueryRow("SELECT SUM(requests) FROM request_log_rollups").Scan(&rollupCount); err != nil || rollupCount != 263 {
		t.Fatalf("cleanup changed rollups: count=%d err=%v", rollupCount, err)
	}
	if err := s.FinalizeRequestLogPrivacy(ctx); err != nil {
		t.Fatal(err)
	}
	var marker string
	if err := s.readDB.QueryRow("SELECT value FROM meta WHERE key=?", requestLogPrivacyKey).Scan(&marker); err != nil || marker != "1" {
		t.Fatalf("missing completed marker: value=%q err=%v", marker, err)
	}
}

func TestRequestLogPrivacyCleanupRollsBackAndRetries(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.writeDB.Exec("DELETE FROM meta WHERE key=?", requestLogPrivacyKey); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"first-secret", "reject-secret"} {
		legacyRequestLog(t, s, "/api/v1/heartbeat/"+suffix, "https://example.test/confirm?token="+suffix)
	}
	query := `CREATE TRIGGER reject_privacy_update BEFORE UPDATE ON request_logs
		WHEN OLD.path LIKE '%reject-secret' BEGIN SELECT RAISE(ABORT,'injected privacy update failure'); END`
	drop := "DROP TRIGGER reject_privacy_update"
	if s.writeDB.dialect == postgresDialect {
		query = `CREATE FUNCTION reject_privacy_update() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF OLD.path LIKE '%reject-secret' THEN RAISE EXCEPTION 'injected privacy update failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_privacy_update BEFORE UPDATE ON request_logs FOR EACH ROW EXECUTE FUNCTION reject_privacy_update()`
		drop += " ON request_logs"
	}
	if _, err := s.writeDB.Exec(query); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequestLogPrivacy(ctx); err == nil {
		t.Fatal("injected cleanup failure was ignored")
	}
	var count int
	if err := s.readDB.QueryRow("SELECT COUNT(*) FROM request_logs WHERE path LIKE '%secret'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed cleanup committed partial edits: count=%d err=%v", count, err)
	}
	var marker string
	if err := s.readDB.QueryRow("SELECT value FROM meta WHERE key=?", requestLogPrivacyKey).Scan(&marker); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed cleanup committed its completion marker: %v", err)
	}
	if _, err := s.writeDB.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequestLogPrivacy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.readDB.QueryRow("SELECT COUNT(*) FROM request_logs WHERE path LIKE '%secret'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("retry did not finish cleanup: count=%d err=%v", count, err)
	}
}

func TestRequestLogPrivacyMarkerFailureRollsBack(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.writeDB.Exec("DELETE FROM meta WHERE key=?", requestLogPrivacyKey); err != nil {
		t.Fatal(err)
	}
	legacyRequestLog(t, s, "/api/v1/heartbeat/marker-private-token", "")
	query := `CREATE TRIGGER reject_privacy_marker BEFORE INSERT ON meta
		WHEN NEW.key='request_log_privacy_v1' BEGIN SELECT RAISE(ABORT,'injected marker failure'); END`
	drop := "DROP TRIGGER reject_privacy_marker"
	if s.writeDB.dialect == postgresDialect {
		query = `CREATE FUNCTION reject_privacy_marker() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.key='request_log_privacy_v1' THEN RAISE EXCEPTION 'injected marker failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_privacy_marker BEFORE INSERT ON meta FOR EACH ROW EXECUTE FUNCTION reject_privacy_marker()`
		drop += " ON meta"
	}
	if _, err := s.writeDB.Exec(query); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequestLogPrivacy(ctx); err == nil {
		t.Fatal("marker failure was ignored")
	}
	var path string
	if err := s.readDB.QueryRow("SELECT path FROM request_logs").Scan(&path); err != nil || path != "/api/v1/heartbeat/marker-private-token" {
		t.Fatalf("marker failure committed cleanup without its marker: path=%q err=%v", path, err)
	}
	if _, err := s.writeDB.Exec(drop); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.FinalizeRequestLogPrivacy(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup did not stop: %v", err)
	}
	if err := s.FinalizeRequestLogPrivacy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.readDB.QueryRow("SELECT path FROM request_logs").Scan(&path); err != nil || path != "/api/v1/heartbeat/[redacted]" {
		t.Fatalf("retry failed: path=%q err=%v", path, err)
	}
}

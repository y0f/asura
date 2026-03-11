package storage

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "asura-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	store, err := NewSQLiteStore(tmpFile.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestMonitorCRUD(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	// Create
	m := &Monitor{
		Name:             "Test HTTP",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		Tags:             []string{"prod"},
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	err := store.CreateMonitor(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Get
	got, err := store.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test HTTP" {
		t.Fatalf("expected 'Test HTTP', got %q", got.Name)
	}
	if got.Status != "pending" {
		t.Fatalf("expected status 'pending', got %q", got.Status)
	}

	// List
	result, err := store.ListMonitors(ctx, MonitorListFilter{}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 monitor, got %d", result.Total)
	}

	// Update
	m.Name = "Updated HTTP"
	m.Tags = []string{"prod", "web"}
	err = store.UpdateMonitor(ctx, m)
	if err != nil {
		t.Fatal(err)
	}

	got, _ = store.GetMonitor(ctx, m.ID)
	if got.Name != "Updated HTTP" {
		t.Fatalf("expected 'Updated HTTP', got %q", got.Name)
	}

	// Pause/Resume
	err = store.SetMonitorEnabled(ctx, m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetMonitor(ctx, m.ID)
	if got.Enabled {
		t.Fatal("expected disabled")
	}

	// Delete
	err = store.DeleteMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetMonitor(ctx, m.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestCheckResults(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{Name: "Test", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, Tags: []string{}, FailureThreshold: 3, SuccessThreshold: 1}
	store.CreateMonitor(ctx, m)

	cr := &CheckResult{
		MonitorID:    m.ID,
		Status:       "up",
		ResponseTime: 150,
		StatusCode:   200,
		Message:      "OK",
		BodyHash:     "abc123",
	}
	err := store.InsertCheckResult(ctx, cr)
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.ListCheckResults(ctx, m.ID, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 result, got %d", result.Total)
	}

	latest, err := store.GetLatestCheckResult(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", latest.StatusCode)
	}
}

func TestIncidents(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{Name: "Test", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, Tags: []string{}, FailureThreshold: 3, SuccessThreshold: 1}
	store.CreateMonitor(ctx, m)

	inc := &Incident{MonitorID: m.ID, Status: "open", Cause: "timeout"}
	err := store.CreateIncident(ctx, inc)
	if err != nil {
		t.Fatal(err)
	}

	open, err := store.GetOpenIncident(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open.ID != inc.ID {
		t.Fatal("wrong open incident")
	}

	// Resolve
	now := time.Now().UTC()
	inc.Status = "resolved"
	inc.ResolvedAt = &now
	inc.ResolvedBy = "test"
	err = store.UpdateIncident(ctx, inc)
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.ListIncidents(ctx, 0, "", "", Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 incident, got %d", result.Total)
	}
}

func TestAnalytics(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{Name: "Test", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, Tags: []string{}, FailureThreshold: 3, SuccessThreshold: 1}
	store.CreateMonitor(ctx, m)

	for i := 0; i < 10; i++ {
		status := "up"
		if i == 5 {
			status = "down"
		}
		store.InsertCheckResult(ctx, &CheckResult{
			MonitorID: m.ID, Status: status, ResponseTime: int64(100 + i*10), StatusCode: 200,
		})
	}

	from := time.Now().Add(-1 * time.Hour)
	to := time.Now().Add(1 * time.Hour)

	uptime, err := store.GetUptimePercent(ctx, m.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if uptime != 90 {
		t.Fatalf("expected 90%% uptime, got %f", uptime)
	}

	p50, p95, p99, err := store.GetResponseTimePercentiles(ctx, m.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if p50 == 0 || p95 == 0 || p99 == 0 {
		t.Fatalf("expected non-zero percentiles: p50=%f p95=%f p99=%f", p50, p95, p99)
	}
}

func createTestHeartbeat(t *testing.T) (*SQLiteStore, context.Context, *Monitor) {
	t.Helper()
	store := testStore(t)
	ctx := context.Background()
	m := &Monitor{Name: "Cron Job", Type: "heartbeat", Target: "heartbeat", Interval: 300, Timeout: 10, Enabled: true, Tags: []string{}, FailureThreshold: 1, SuccessThreshold: 1}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	hb := &Heartbeat{
		MonitorID: m.ID,
		Token:     "abc123def456",
		Grace:     60,
		Status:    "pending",
	}
	if err := store.CreateHeartbeat(ctx, hb); err != nil {
		t.Fatal(err)
	}
	if hb.ID == 0 {
		t.Fatal("expected non-zero heartbeat ID")
	}
	return store, ctx, m
}

func TestHeartbeatCRUD(t *testing.T) {
	store, ctx, m := createTestHeartbeat(t)

	t.Run("GetByToken", func(t *testing.T) {
		got, err := store.GetHeartbeatByToken(ctx, "abc123def456")
		if err != nil {
			t.Fatal(err)
		}
		if got.MonitorID != m.ID {
			t.Fatalf("expected monitor_id %d, got %d", m.ID, got.MonitorID)
		}
		if got.Grace != 60 {
			t.Fatalf("expected grace 60, got %d", got.Grace)
		}
	})

	t.Run("GetByMonitorID", func(t *testing.T) {
		got, err := store.GetHeartbeatByMonitorID(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "abc123def456" {
			t.Fatalf("expected token abc123def456, got %s", got.Token)
		}
	})

	t.Run("UpdatePing", func(t *testing.T) {
		if err := store.UpdateHeartbeatPing(ctx, "abc123def456"); err != nil {
			t.Fatal(err)
		}
		got, _ := store.GetHeartbeatByToken(ctx, "abc123def456")
		if got.Status != "up" {
			t.Fatalf("expected status up after ping, got %s", got.Status)
		}
		if got.LastPingAt == nil {
			t.Fatal("expected last_ping_at to be set")
		}
	})

	t.Run("UpdateStatus", func(t *testing.T) {
		if err := store.UpdateHeartbeatStatus(ctx, m.ID, "down"); err != nil {
			t.Fatal(err)
		}
		got, _ := store.GetHeartbeatByToken(ctx, "abc123def456")
		if got.Status != "down" {
			t.Fatalf("expected status down, got %s", got.Status)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := store.DeleteHeartbeat(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		_, err := store.GetHeartbeatByToken(ctx, "abc123def456")
		if err == nil {
			t.Fatal("expected error after delete")
		}
	})
}

func TestIsMonitorOnStatusPage(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{Name: "Test", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, Tags: []string{}, FailureThreshold: 3, SuccessThreshold: 1}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}

	// Not on any status page yet
	visible, err := store.IsMonitorOnStatusPage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if visible {
		t.Fatal("expected not visible before assignment")
	}

	// Create an enabled status page and assign the monitor
	sp := &StatusPage{Title: "Test Page", Slug: "test", Enabled: true}
	if err := store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatusPageMonitors(ctx, sp.ID, []StatusPageMonitor{{PageID: sp.ID, MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}

	visible, err = store.IsMonitorOnStatusPage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !visible {
		t.Fatal("expected visible after assignment to enabled page")
	}

	// Disable the page — monitor should no longer be visible
	sp.Enabled = false
	if err := store.UpdateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	visible, _ = store.IsMonitorOnStatusPage(ctx, m.ID)
	if visible {
		t.Fatal("expected not visible when page disabled")
	}
}

func TestSessionCRUD(t *testing.T) {
	t.Run("CreateAndGet", testSessionCreateAndGet)
	t.Run("GetNotFound", testSessionGetNotFound)
	t.Run("Delete", testSessionDelete)
	t.Run("DeleteExpired", testSessionDeleteExpired)
	t.Run("DeleteByAPIKeyName", testSessionDeleteByAPIKeyName)
	t.Run("DeleteExceptKeyNames", testSessionDeleteExceptKeyNames)
	t.Run("DeleteExceptKeyNamesEmpty", testSessionDeleteExceptEmpty)
	t.Run("KeyHashStored", testSessionKeyHashStored)
}

func testSessionCreateAndGet(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	sess := &Session{
		TokenHash: "abc123hash", APIKeyName: "admin",
		IPAddress: "192.168.1.1", ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if sess.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	got, err := store.GetSessionByTokenHash(ctx, "abc123hash")
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKeyName != "admin" {
		t.Fatalf("expected api_key_name 'admin', got %q", got.APIKeyName)
	}
	if got.IPAddress != "192.168.1.1" {
		t.Fatalf("expected ip '192.168.1.1', got %q", got.IPAddress)
	}
}

func testSessionGetNotFound(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	_, err := store.GetSessionByTokenHash(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func testSessionDelete(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.CreateSession(ctx, &Session{TokenHash: "deleteme", APIKeyName: "admin", ExpiresAt: time.Now().Add(24 * time.Hour)})
	if err := store.DeleteSession(ctx, "deleteme"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionByTokenHash(ctx, "deleteme"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func testSessionDeleteExpired(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.CreateSession(ctx, &Session{TokenHash: "expired_token", APIKeyName: "admin", ExpiresAt: time.Now().Add(-1 * time.Hour)})
	store.CreateSession(ctx, &Session{TokenHash: "valid_token", APIKeyName: "admin", ExpiresAt: time.Now().Add(24 * time.Hour)})

	deleted, err := store.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("expected at least 1 expired session deleted")
	}
	if _, err = store.GetSessionByTokenHash(ctx, "expired_token"); err == nil {
		t.Fatal("expected expired session to be deleted")
	}
	got, err := store.GetSessionByTokenHash(ctx, "valid_token")
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenHash != "valid_token" {
		t.Fatal("valid session should still exist")
	}
}

func testSessionDeleteByAPIKeyName(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.CreateSession(ctx, &Session{TokenHash: "key1_sess1", APIKeyName: "key1", ExpiresAt: time.Now().Add(24 * time.Hour)})
	store.CreateSession(ctx, &Session{TokenHash: "key1_sess2", APIKeyName: "key1", ExpiresAt: time.Now().Add(24 * time.Hour)})
	store.CreateSession(ctx, &Session{TokenHash: "key2_sess1", APIKeyName: "key2", ExpiresAt: time.Now().Add(24 * time.Hour)})

	deleted, err := store.DeleteSessionsByAPIKeyName(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}
	if _, err = store.GetSessionByTokenHash(ctx, "key1_sess1"); err == nil {
		t.Fatal("expected key1 session to be deleted")
	}
	got, err := store.GetSessionByTokenHash(ctx, "key2_sess1")
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKeyName != "key2" {
		t.Fatal("key2 session should still exist")
	}
}

func testSessionDeleteExceptKeyNames(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.CreateSession(ctx, &Session{TokenHash: "admin_tok", APIKeyName: "admin", ExpiresAt: time.Now().Add(24 * time.Hour)})
	store.CreateSession(ctx, &Session{TokenHash: "readonly_tok", APIKeyName: "readonly", ExpiresAt: time.Now().Add(24 * time.Hour)})
	store.CreateSession(ctx, &Session{TokenHash: "removed_tok", APIKeyName: "removed", ExpiresAt: time.Now().Add(24 * time.Hour)})

	deleted, err := store.DeleteSessionsExceptKeyNames(ctx, []string{"admin", "readonly"})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	if _, err = store.GetSessionByTokenHash(ctx, "removed_tok"); err == nil {
		t.Fatal("expected removed key session to be deleted")
	}
	if _, err := store.GetSessionByTokenHash(ctx, "admin_tok"); err != nil {
		t.Fatal("admin session should still exist")
	}
	if _, err := store.GetSessionByTokenHash(ctx, "readonly_tok"); err != nil {
		t.Fatal("readonly session should still exist")
	}
}

func testSessionDeleteExceptEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.CreateSession(ctx, &Session{TokenHash: "tok1", APIKeyName: "any", ExpiresAt: time.Now().Add(24 * time.Hour)})
	deleted, err := store.DeleteSessionsExceptKeyNames(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
}

func testSessionKeyHashStored(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.CreateSession(ctx, &Session{
		TokenHash: "hash_test_tok", APIKeyName: "admin",
		KeyHash: "abcdef123456", ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	got, err := store.GetSessionByTokenHash(ctx, "hash_test_tok")
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyHash != "abcdef123456" {
		t.Fatalf("expected key_hash 'abcdef123456', got %q", got.KeyHash)
	}
}

func TestRequestLogBatchInsertAndList(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	mid := int64(1)
	logs := []*RequestLog{
		{Method: "GET", Path: "/", StatusCode: 200, LatencyMs: 5, ClientIP: "aaa", UserAgent: "Mozilla/5.0", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/api/v1/monitors", StatusCode: 200, LatencyMs: 12, ClientIP: "bbb", RouteGroup: "api", CreatedAt: now},
		{Method: "GET", Path: "/api/v1/badge/1/status", StatusCode: 200, LatencyMs: 3, ClientIP: "aaa", MonitorID: &mid, RouteGroup: "badge", CreatedAt: now},
		{Method: "POST", Path: "/login", StatusCode: 303, LatencyMs: 80, ClientIP: "ccc", RouteGroup: "auth", CreatedAt: now},
	}

	if err := store.InsertRequestLogBatch(ctx, logs); err != nil {
		t.Fatal(err)
	}

	// List all
	result, err := store.ListRequestLogs(ctx, RequestLogFilter{}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 4 {
		t.Fatalf("expected 4 logs, got %d", result.Total)
	}
	entries := result.Data.([]*RequestLog)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// Filter by route group
	result, err = store.ListRequestLogs(ctx, RequestLogFilter{RouteGroup: "api"}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 api log, got %d", result.Total)
	}

	// Filter by method
	result, err = store.ListRequestLogs(ctx, RequestLogFilter{Method: "POST"}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 POST log, got %d", result.Total)
	}

	// Filter by monitor_id
	result, err = store.ListRequestLogs(ctx, RequestLogFilter{MonitorID: &mid}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 monitor-linked log, got %d", result.Total)
	}
	entry := result.Data.([]*RequestLog)[0]
	if entry.MonitorID == nil || *entry.MonitorID != 1 {
		t.Fatal("expected monitor_id=1")
	}
}

func TestRequestLogStats(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	logs := []*RequestLog{
		{Method: "GET", Path: "/", StatusCode: 200, LatencyMs: 10, ClientIP: "aaa", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/monitors", StatusCode: 200, LatencyMs: 20, ClientIP: "aaa", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/api/v1/monitors", StatusCode: 200, LatencyMs: 30, ClientIP: "bbb", RouteGroup: "api", CreatedAt: now},
	}
	if err := store.InsertRequestLogBatch(ctx, logs); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetRequestLogStats(ctx, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRequests != 3 {
		t.Fatalf("expected 3 total requests, got %d", stats.TotalRequests)
	}
	if stats.UniqueVisitors != 2 {
		t.Fatalf("expected 2 unique visitors, got %d", stats.UniqueVisitors)
	}
	if stats.AvgLatencyMs != 20 {
		t.Fatalf("expected avg latency 20, got %d", stats.AvgLatencyMs)
	}
	if len(stats.TopPaths) < 1 {
		t.Fatal("expected at least 1 top path")
	}
}

func TestRequestLogRollup(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	date := "2025-01-15"
	ts, _ := time.Parse("2006-01-02T15:04:05Z", date+"T12:00:00Z")
	logs := []*RequestLog{
		{Method: "GET", Path: "/", StatusCode: 200, LatencyMs: 10, ClientIP: "aaa", RouteGroup: "web", CreatedAt: ts},
		{Method: "GET", Path: "/", StatusCode: 200, LatencyMs: 20, ClientIP: "bbb", RouteGroup: "web", CreatedAt: ts},
		{Method: "GET", Path: "/api/v1/health", StatusCode: 200, LatencyMs: 5, ClientIP: "aaa", RouteGroup: "api", CreatedAt: ts},
	}
	if err := store.InsertRequestLogBatch(ctx, logs); err != nil {
		t.Fatal(err)
	}

	if err := store.RollupRequestLogs(ctx, date); err != nil {
		t.Fatal(err)
	}

	// Verify rollup data exists
	var count int
	err := store.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_log_rollups WHERE date=?", date).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count < 1 {
		t.Fatal("expected at least 1 rollup row")
	}

	// Running rollup again should not error (INSERT OR REPLACE)
	if err := store.RollupRequestLogs(ctx, date); err != nil {
		t.Fatal("second rollup should not error:", err)
	}
}

func TestRequestLogPurge(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	old := time.Now().UTC().AddDate(0, 0, -60)
	recent := time.Now().UTC()

	logs := []*RequestLog{
		{Method: "GET", Path: "/old", StatusCode: 200, LatencyMs: 5, ClientIP: "aaa", RouteGroup: "web", CreatedAt: old},
		{Method: "GET", Path: "/new", StatusCode: 200, LatencyMs: 5, ClientIP: "bbb", RouteGroup: "web", CreatedAt: recent},
	}
	if err := store.InsertRequestLogBatch(ctx, logs); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.PurgeOldRequestLogs(ctx, time.Now().UTC().AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	result, err := store.ListRequestLogs(ctx, RequestLogFilter{}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 remaining log, got %d", result.Total)
	}
}

func TestListTopClientIPs(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	logs := []*RequestLog{
		{Method: "GET", Path: "/", StatusCode: 200, LatencyMs: 5, ClientIP: "1.1.1.1", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/a", StatusCode: 200, LatencyMs: 5, ClientIP: "1.1.1.1", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/b", StatusCode: 200, LatencyMs: 5, ClientIP: "1.1.1.1", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/c", StatusCode: 200, LatencyMs: 5, ClientIP: "2.2.2.2", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/d", StatusCode: 200, LatencyMs: 5, ClientIP: "2.2.2.2", RouteGroup: "web", CreatedAt: now},
		{Method: "GET", Path: "/e", StatusCode: 200, LatencyMs: 5, ClientIP: "3.3.3.3", RouteGroup: "web", CreatedAt: now},
	}
	if err := store.InsertRequestLogBatch(ctx, logs); err != nil {
		t.Fatal(err)
	}

	ips, err := store.ListTopClientIPs(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 3 {
		t.Fatalf("expected 3 IPs, got %d", len(ips))
	}
	// First entry must be the most frequent IP
	if ips[0] != "1.1.1.1" {
		t.Fatalf("expected 1.1.1.1 first, got %s", ips[0])
	}

	// Respect limit
	ips, err = store.ListTopClientIPs(ctx, now.Add(-time.Hour), now.Add(time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP with limit=1, got %d", len(ips))
	}

	// Empty range returns nothing
	ips, err = store.ListTopClientIPs(ctx, now.Add(-2*time.Hour), now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected 0 IPs outside range, got %d", len(ips))
	}
}

func TestInsertRequestLogBatchEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.InsertRequestLogBatch(ctx, nil); err != nil {
		t.Fatal("empty batch should not error:", err)
	}
	if err := store.InsertRequestLogBatch(ctx, []*RequestLog{}); err != nil {
		t.Fatal("empty slice should not error:", err)
	}
}

func TestMigrationFromOldVersionFails(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "asura-migrate-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	db, err := sql.Open("sqlite", tmpFile.Name()+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL); INSERT INTO schema_version (version) VALUES (1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = NewSQLiteStore(tmpFile.Name(), 2)
	if err == nil {
		t.Fatal("expected error for pre-v1.0.0 database")
	}
	if !strings.Contains(err.Error(), "too old") {
		t.Fatalf("expected 'too old' error, got: %v", err)
	}
}

func TestMigrationFreshDB(t *testing.T) {
	store := testStore(t)

	var version int
	if err := store.writeDB.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("fresh DB: expected version %d, got %d", schemaVersion, version)
	}

	// Verify status_pages table exists
	var spCount int
	if err := store.readDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='status_pages'").Scan(&spCount); err != nil {
		t.Fatal(err)
	}
	if spCount != 1 {
		t.Fatal("fresh DB: status_pages table missing")
	}
}

func TestMigrationIdempotent(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "asura-idem-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	// Open twice — second open should be a no-op
	s1, err := NewSQLiteStore(tmpFile.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := NewSQLiteStore(tmpFile.Name(), 2)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	defer s2.Close()

	var version int
	if err := s2.writeDB.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("expected version %d after re-open, got %d", schemaVersion, version)
	}
}

func TestTOTPKeyCRUD(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	t.Run("CreateAndGet", func(t *testing.T) {
		key := &TOTPKey{
			APIKeyName: "admin",
			Secret:     "JBSWY3DPEHPK3PXP",
		}
		if err := store.CreateTOTPKey(ctx, key); err != nil {
			t.Fatal(err)
		}
		if key.ID == 0 {
			t.Fatal("expected non-zero ID")
		}

		got, err := store.GetTOTPKey(ctx, "admin")
		if err != nil {
			t.Fatal(err)
		}
		if got.APIKeyName != "admin" {
			t.Fatalf("expected api_key_name 'admin', got %q", got.APIKeyName)
		}
		if got.Secret != "JBSWY3DPEHPK3PXP" {
			t.Fatalf("expected secret preserved, got %q", got.Secret)
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := store.GetTOTPKey(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent TOTP key")
		}
	})

	t.Run("DuplicateConstraint", func(t *testing.T) {
		key := &TOTPKey{
			APIKeyName: "admin",
			Secret:     "DIFFERENT",
		}
		err := store.CreateTOTPKey(ctx, key)
		if err == nil {
			t.Fatal("expected error for duplicate api_key_name")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := store.DeleteTOTPKey(ctx, "admin"); err != nil {
			t.Fatal(err)
		}
		_, err := store.GetTOTPKey(ctx, "admin")
		if err == nil {
			t.Fatal("expected error after deletion")
		}
	})
}

func TestProxyCRUD(t *testing.T) {
	t.Run("CreateGetListUpdateDelete", testProxyLifecycle)
	t.Run("AssignToMonitor", testProxyAssignToMonitor)
	t.Run("NotFound", testProxyNotFound)
}

func testProxyLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	p := &Proxy{
		Name: "Test Proxy", Protocol: "http", Host: "proxy.example.com",
		Port: 8080, AuthUser: "user", AuthPass: "pass", Enabled: true,
	}
	if err := store.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := store.GetProxy(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test Proxy" || got.Host != "proxy.example.com" || got.Port != 8080 {
		t.Fatalf("get mismatch: %+v", got)
	}

	proxies, err := store.ListProxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(proxies))
	}

	p.Name = "Updated Proxy"
	p.Protocol = "socks5"
	p.Port = 1080
	if err := store.UpdateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetProxy(ctx, p.ID)
	if got.Name != "Updated Proxy" || got.Protocol != "socks5" || got.Port != 1080 {
		t.Fatalf("update mismatch: %+v", got)
	}

	if err := store.DeleteProxy(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	proxies, _ = store.ListProxies(ctx)
	if len(proxies) != 0 {
		t.Fatalf("expected 0 proxies after delete, got %d", len(proxies))
	}
}

func testProxyAssignToMonitor(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	p := &Proxy{Name: "Proxy", Protocol: "http", Host: "proxy.example.com", Port: 8080, Enabled: true}
	if err := store.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{
		Name: "Proxied Monitor", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true, Tags: []string{},
		FailureThreshold: 3, SuccessThreshold: 1, ProxyID: &p.ID,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	mon, _ := store.GetMonitor(ctx, m.ID)
	if mon.ProxyID == nil || *mon.ProxyID != p.ID {
		t.Fatalf("expected proxy_id=%d, got %v", p.ID, mon.ProxyID)
	}
}

func testProxyNotFound(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	_, err := store.GetProxy(ctx, 99999)
	if err == nil {
		t.Fatal("expected error for nonexistent proxy")
	}
}

func TestTags(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	t1 := &Tag{Name: "web", Color: "#ff0000"}
	t2 := &Tag{Name: "prod", Color: "#00ff00"}
	t3 := &Tag{Name: "api", Color: "#0000ff"}
	if err := store.CreateTag(ctx, t1); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTag(ctx, t2); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTag(ctx, t3); err != nil {
		t.Fatal(err)
	}

	tags, err := store.ListTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d: %v", len(tags), tags)
	}

	got, err := store.GetTag(ctx, t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "web" || got.Color != "#ff0000" {
		t.Errorf("GetTag: got %+v", got)
	}

	t1.Name = "frontend"
	if err := store.UpdateTag(ctx, t1); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetTag(ctx, t1.ID)
	if got.Name != "frontend" {
		t.Errorf("after update: name = %q", got.Name)
	}

	m := createTestMonitor(t, store, ctx, "TagMon")
	monTags := []MonitorTag{
		{TagID: t1.ID, Value: "val1"},
		{TagID: t2.ID, Value: ""},
	}
	if err := store.SetMonitorTags(ctx, m.ID, monTags); err != nil {
		t.Fatal(err)
	}

	gotMT, err := store.GetMonitorTags(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotMT) != 2 {
		t.Fatalf("expected 2 monitor tags, got %d", len(gotMT))
	}

	batchMap, err := store.GetMonitorTagsBatch(ctx, []int64{m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(batchMap[m.ID]) != 2 {
		t.Errorf("batch: expected 2 tags for monitor %d, got %d", m.ID, len(batchMap[m.ID]))
	}

	if err := store.DeleteTag(ctx, t3.ID); err != nil {
		t.Fatal(err)
	}
	tags, _ = store.ListTags(ctx)
	if len(tags) != 2 {
		t.Errorf("after delete: expected 2 tags, got %d", len(tags))
	}
}

func createTestMonitor(t *testing.T, store *SQLiteStore, ctx context.Context, name string) *Monitor {
	t.Helper()
	m := &Monitor{
		Name: name, Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBulkSetMonitorsEnabled(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m1 := createTestMonitor(t, store, ctx, "Mon1")
	m2 := createTestMonitor(t, store, ctx, "Mon2")
	m3 := createTestMonitor(t, store, ctx, "Mon3")

	affected, err := store.BulkSetMonitorsEnabled(ctx, []int64{m1.ID, m2.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("expected 2 affected, got %d", affected)
	}

	got1, _ := store.GetMonitor(ctx, m1.ID)
	got2, _ := store.GetMonitor(ctx, m2.ID)
	got3, _ := store.GetMonitor(ctx, m3.ID)
	if got1.Enabled {
		t.Error("m1 should be disabled")
	}
	if got2.Enabled {
		t.Error("m2 should be disabled")
	}
	if !got3.Enabled {
		t.Error("m3 should still be enabled")
	}

	affected, err = store.BulkSetMonitorsEnabled(ctx, []int64{m1.ID, m2.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("expected 2 affected, got %d", affected)
	}
}

func TestBulkSetMonitorsEnabledEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	affected, err := store.BulkSetMonitorsEnabled(ctx, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 0 {
		t.Fatalf("expected 0 affected, got %d", affected)
	}
}

func TestBulkDeleteMonitors(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m1 := createTestMonitor(t, store, ctx, "Del1")
	m2 := createTestMonitor(t, store, ctx, "Del2")
	m3 := createTestMonitor(t, store, ctx, "Del3")

	affected, err := store.BulkDeleteMonitors(ctx, []int64{m1.ID, m3.ID})
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("expected 2 affected, got %d", affected)
	}

	_, err = store.GetMonitor(ctx, m1.ID)
	if !strings.Contains(err.Error(), "no rows") {
		t.Error("m1 should be deleted")
	}
	got2, err := store.GetMonitor(ctx, m2.ID)
	if err != nil {
		t.Fatal("m2 should still exist:", err)
	}
	if got2.Name != "Del2" {
		t.Error("m2 name mismatch")
	}
}

func TestBulkSetMonitorGroup(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	g := &MonitorGroup{Name: "TestGroup"}
	if err := store.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}

	m1 := createTestMonitor(t, store, ctx, "Grp1")
	m2 := createTestMonitor(t, store, ctx, "Grp2")

	affected, err := store.BulkSetMonitorGroup(ctx, []int64{m1.ID, m2.ID}, &g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("expected 2 affected, got %d", affected)
	}

	got1, _ := store.GetMonitor(ctx, m1.ID)
	got2, _ := store.GetMonitor(ctx, m2.ID)
	if got1.GroupID == nil || *got1.GroupID != g.ID {
		t.Error("m1 should be in group")
	}
	if got2.GroupID == nil || *got2.GroupID != g.ID {
		t.Error("m2 should be in group")
	}

	affected, err = store.BulkSetMonitorGroup(ctx, []int64{m1.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("expected 1 affected, got %d", affected)
	}
	got1, _ = store.GetMonitor(ctx, m1.ID)
	if got1.GroupID != nil {
		t.Error("m1 should have no group")
	}
}

func TestMonitorSLATarget(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{
		Name:             "SLA Monitor",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		Tags:             []string{},
		SLATarget:        99.9,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SLATarget != 99.9 {
		t.Fatalf("expected SLATarget 99.9, got %v", got.SLATarget)
	}

	m.SLATarget = 99.99
	if err := store.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetMonitor(ctx, m.ID)
	if got.SLATarget != 99.99 {
		t.Fatalf("expected SLATarget 99.99, got %v", got.SLATarget)
	}

	m.SLATarget = 0
	if err := store.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetMonitor(ctx, m.ID)
	if got.SLATarget != 0 {
		t.Fatalf("expected SLATarget 0, got %v", got.SLATarget)
	}

	result, err := store.ListMonitors(ctx, MonitorListFilter{}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	monList := result.Data.([]*Monitor)
	if len(monList) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(monList))
	}
	if monList[0].SLATarget != 0 {
		t.Fatalf("expected SLATarget 0 in list, got %v", monList[0].SLATarget)
	}
}

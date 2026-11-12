package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/y0f/asura/v3/internal/api"
)

func seedTestData(t *testing.T, srv *Server, adminKey string) {
	t.Helper()

	post(t, srv, adminKey, "/api/v1/groups", map[string]any{
		"name": "Backend", "sort_order": 1,
	}, http.StatusCreated)

	post(t, srv, adminKey, "/api/v1/proxies", map[string]any{
		"name": "US Proxy", "protocol": "http", "host": "proxy.example.com", "port": 8080, "enabled": true,
	}, http.StatusCreated)

	post(t, srv, adminKey, "/api/v1/notifications", map[string]any{
		"name": "Slack Alerts", "type": "webhook", "enabled": true,
		"settings": map[string]any{"url": "https://hooks.slack.com/test"},
		"events":   []string{"incident.created", "incident.resolved"},
	}, http.StatusCreated)

	post(t, srv, adminKey, "/api/v1/monitors", map[string]any{
		"name": "API Health", "type": "http", "target": "https://api.example.com/health",
		"interval": 30, "timeout": 5, "group_id": 1, "proxy_id": 1,
		"notification_channel_ids": []int{1},
	}, http.StatusCreated)

	post(t, srv, adminKey, "/api/v1/monitors", map[string]any{
		"name": "Web Frontend", "type": "http", "target": "https://example.com",
		"interval": 60, "timeout": 10,
	}, http.StatusCreated)

	post(t, srv, adminKey, "/api/v1/status-pages", map[string]any{
		"slug": "public", "title": "Public Status", "enabled": true,
		"monitors": []map[string]any{
			{"monitor_id": 1, "sort_order": 1},
			{"monitor_id": 2, "sort_order": 2},
		},
	}, http.StatusCreated)
}

func post(t *testing.T, srv *Server, key, path string, body map[string]any, expectStatus int) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != expectStatus {
		t.Fatalf("POST %s: expected %d, got %d: %s", path, expectStatus, w.Code, w.Body.String())
	}
}

func getExport(t *testing.T, srv *Server, adminKey, query string) api.ConfigurationFile {
	t.Helper()
	url := "/api/v1/export"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("X-API-Key", adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var data api.ConfigurationFile
	if err := json.NewDecoder(w.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return data
}

func doImport(t *testing.T, srv *Server, adminKey string, exportJSON []byte, mode string) api.ImportStats {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/import?mode="+mode, bytes.NewReader(exportJSON))
	req.Header.Set("X-API-Key", adminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("import: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var stats api.ImportStats
	json.NewDecoder(w.Body).Decode(&stats)
	return stats
}

func getRawExport(t *testing.T, srv *Server, adminKey string) []byte {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/export", nil)
	req.Header.Set("X-API-Key", adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestExportEmpty(t *testing.T) {
	srv, adminKey := testServer(t)

	data := getExport(t, srv, adminKey, "")
	if data.Version != 2 {
		t.Fatalf("expected version 2, got %d", data.Version)
	}
	if len(data.Monitors) != 0 {
		t.Fatalf("expected 0 monitors, got %d", len(data.Monitors))
	}
}

func TestExportWithData(t *testing.T) {
	srv, adminKey := testServer(t)
	seedTestData(t, srv, adminKey)

	data := getExport(t, srv, adminKey, "")

	if len(data.Monitors) != 2 {
		t.Fatalf("expected 2 monitors, got %d", len(data.Monitors))
	}

	mon := data.Monitors[0]
	if mon.Name != "API Health" {
		t.Fatalf("expected 'API Health', got %q", mon.Name)
	}
	if mon.GroupID == nil || *mon.GroupID != data.MonitorGroups[0].ID || data.MonitorGroups[0].Name != "Backend" {
		t.Fatal("group reference was not preserved")
	}
	if mon.ProxyID == nil || *mon.ProxyID != data.Proxies[0].ID || data.Proxies[0].Name != "US Proxy" {
		t.Fatal("proxy reference was not preserved")
	}
	if len(mon.NotificationChannelIDs) != 1 || mon.NotificationChannelIDs[0] != data.NotificationChannels[0].ID || data.NotificationChannels[0].Name != "Slack Alerts" {
		t.Fatal("channel reference was not preserved")
	}

	counts := map[string]int{
		"groups":   len(data.MonitorGroups),
		"proxies":  len(data.Proxies),
		"channels": len(data.NotificationChannels),
		"pages":    len(data.StatusPages),
	}
	for k, v := range counts {
		if v != 1 {
			t.Fatalf("expected 1 %s, got %d", k, v)
		}
	}
	if len(data.StatusPages[0].Monitors) != 2 {
		t.Fatalf("expected 2 status page monitors, got %d", len(data.StatusPages[0].Monitors))
	}
}

func TestExportRedactSecrets(t *testing.T) {
	srv, adminKey := testServer(t)
	seedTestData(t, srv, adminKey)

	data := getExport(t, srv, adminKey, "redact_secrets=true")

	if string(data.NotificationChannels[0].Settings) != "{}" {
		t.Fatalf("expected redacted channel settings, got %s", data.NotificationChannels[0].Settings)
	}
	if data.Proxies[0].AuthUser != "" || data.Proxies[0].AuthPass != "" {
		t.Fatal("expected redacted proxy credentials")
	}
}

func TestImportIntoEmpty(t *testing.T) {
	srv, adminKey := testServer(t)
	seedTestData(t, srv, adminKey)
	exportJSON := getRawExport(t, srv, adminKey)

	srv2, adminKey2 := testServer(t)
	stats := doImport(t, srv2, adminKey2, exportJSON, "merge")

	expected := map[string]int{
		"groups": 1, "proxies": 1, "channels": 1,
		"monitors": 2, "pages": 1, "skipped": 0, "errors": 0,
	}
	actual := map[string]int{
		"groups": stats.Groups, "proxies": stats.Proxies, "channels": stats.Channels,
		"monitors": stats.Monitors, "pages": stats.StatusPages, "skipped": stats.Skipped, "errors": stats.Errors,
	}
	for k, want := range expected {
		if got := actual[k]; got != want {
			t.Fatalf("%s: expected %d, got %d", k, want, got)
		}
	}

	data := getExport(t, srv2, adminKey2, "")
	if len(data.Monitors) != 2 {
		t.Fatalf("re-export: expected 2 monitors, got %d", len(data.Monitors))
	}
	if data.Monitors[0].GroupID == nil || *data.Monitors[0].GroupID != data.MonitorGroups[0].ID {
		t.Fatal("re-export lost group reference")
	}
	if data.Monitors[0].ProxyID == nil || *data.Monitors[0].ProxyID != data.Proxies[0].ID {
		t.Fatal("re-export lost proxy reference")
	}
}

func TestImportMergeSkipsDuplicates(t *testing.T) {
	srv, adminKey := testServer(t)
	seedTestData(t, srv, adminKey)
	exportJSON := getRawExport(t, srv, adminKey)

	stats := doImport(t, srv, adminKey, exportJSON, "merge")

	if stats.Monitors != 0 {
		t.Fatalf("expected 0 monitors (all skipped), got %d", stats.Monitors)
	}
	if stats.Skipped != 6 {
		t.Fatalf("expected 6 skipped (1 group + 1 proxy + 1 channel + 2 monitors + 1 page), got %d", stats.Skipped)
	}
}

func TestImportInvalidVersion(t *testing.T) {
	srv, adminKey := testServer(t)

	body, _ := json.Marshal(api.ExportData{Version: 99})
	req := httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(body))
	req.Header.Set("X-API-Key", adminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImportInvalidMode(t *testing.T) {
	srv, adminKey := testServer(t)

	body, _ := json.Marshal(api.ExportData{Version: 1})
	req := httptest.NewRequest("POST", "/api/v1/import?mode=invalid", bytes.NewReader(body))
	req.Header.Set("X-API-Key", adminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestExportRequiresAuth(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/export", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestImportRequiresWritePermission(t *testing.T) {
	srv, _ := testServer(t)
	readKey := "test-read-key"

	body, _ := json.Marshal(api.ExportData{Version: 1})
	req := httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(body))
	req.Header.Set("X-API-Key", readKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestExportContentDisposition(t *testing.T) {
	srv, adminKey := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/export", nil)
	req.Header.Set("X-API-Key", adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="asura-export.json"` {
		t.Fatalf("expected Content-Disposition attachment, got %q", cd)
	}
}

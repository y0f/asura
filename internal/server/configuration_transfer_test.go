package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/api"
)

func transferRequest(srv *Server, key string, body []byte, mode string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/import?mode="+mode, bytes.NewReader(body))
	r.Header.Set("X-API-Key", key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func transferFileMonitor(id int64, name string) api.ConfigurationMonitor {
	return api.ConfigurationMonitor{ID: id, Name: name, Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1}
}

func TestConfigurationHTTPRejectsPartialAndRedactedImports(t *testing.T) {
	srv, key := testServer(t)
	t.Cleanup(srv.Close)
	seedTestData(t, srv, key)
	before := getExport(t, srv, key, "")
	invalid := api.ConfigurationFile{Version: 2, MonitorGroups: []api.ConfigurationGroup{{ID: 1, Name: "must roll back"}}, Monitors: []api.ConfigurationMonitor{transferFileMonitor(1, "valid"), transferFileMonitor(2, "invalid")}}
	invalid.Monitors[1].Timeout = -1
	for _, mode := range []string{"merge", "replace"} {
		body, _ := json.Marshal(invalid)
		w := transferRequest(srv, key, body, mode)
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid %s import=%d %s", mode, w.Code, w.Body.String())
		}
		after := getExport(t, srv, key, "")
		if len(after.Monitors) != len(before.Monitors) || len(after.MonitorGroups) != len(before.MonitorGroups) || after.Monitors[0].Name != before.Monitors[0].Name {
			t.Fatal("failed HTTP import partially changed configuration")
		}
	}
	redacted := getExport(t, srv, key, "redact_secrets=true")
	body, _ := json.Marshal(redacted)
	if w := transferRequest(srv, key, body, "replace"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "sharing only") {
		t.Fatalf("redacted import=%d %s", w.Code, w.Body.String())
	}
}

func TestConfigurationHTTPReplaceClearsAllPortableEntities(t *testing.T) {
	srv, key := testServer(t)
	t.Cleanup(srv.Close)
	seedTestData(t, srv, key)
	body, _ := json.Marshal(api.ConfigurationFile{Version: 2})
	if w := transferRequest(srv, key, body, "replace"); w.Code != http.StatusOK {
		t.Fatalf("replace=%d %s", w.Code, w.Body.String())
	}
	got := getExport(t, srv, key, "")
	if len(got.Monitors)+len(got.NotificationChannels)+len(got.MonitorGroups)+len(got.Proxies)+len(got.StatusPages) != 0 {
		t.Fatal("replace retained portable entities")
	}
	// YAML authority is unchanged: the same key can create new configuration.
	post(t, srv, key, "/api/v1/groups", map[string]any{"name": "after replace"}, http.StatusCreated)
}

func TestConfigurationHTTPImportExceedsNormalSmallRequestLimit(t *testing.T) {
	srv, key := testServer(t)
	t.Cleanup(srv.Close)
	file := api.ConfigurationFile{Version: 2}
	settings, _ := json.Marshal(map[string]string{"body": strings.Repeat("x", 1024)})
	for i := range 1000 {
		m := transferFileMonitor(int64(i+1), fmt.Sprintf("imported %d", i))
		m.Settings = settings
		file.Monitors = append(file.Monitors, m)
	}
	body, _ := json.Marshal(file)
	if len(body) <= 1<<20 {
		t.Fatal("fixture does not exceed the normal request limit")
	}
	w := transferRequest(srv, key, body, "merge")
	if w.Code != http.StatusOK {
		t.Fatalf("bounded large import rejected: %d %s", w.Code, w.Body.String())
	}
	got := getExport(t, srv, key, "")
	if len(got.Monitors) != 1000 {
		t.Fatalf("large import/export truncated: %d", len(got.Monitors))
	}
}

func TestConfigurationHTTPHeartbeatCredentialsAreOneTime(t *testing.T) {
	srv, key := testServer(t)
	t.Cleanup(srv.Close)
	grace := 37
	m := transferFileMonitor(31, "nightly backup")
	m.Type, m.Target, m.HeartbeatGrace = "heartbeat", "heartbeat", &grace
	file := api.ConfigurationFile{Version: 2, Monitors: []api.ConfigurationMonitor{m}}
	body, _ := json.Marshal(file)
	w := transferRequest(srv, key, body, "merge")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("heartbeat import=%d %s", w.Code, w.Body.String())
	}
	var result api.ImportStats
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Heartbeats) != 1 || len(result.Heartbeats[0].Token) != 64 {
		t.Fatalf("one-time enrollment missing: %v", err)
	}
	token := result.Heartbeats[0].Token
	hb, err := srv.store.GetHeartbeatByToken(context.Background(), token)
	if err != nil || hb.Grace != grace || hb.MonitorID != result.Heartbeats[0].MonitorID {
		t.Fatalf("credential not usable: %v", err)
	}
	exported := getRawExport(t, srv, key)
	if bytes.Contains(exported, []byte(token)) || bytes.Contains(exported, []byte(hb.Token)) {
		t.Fatal("export leaked enrollment credential or hash")
	}
	w = transferRequest(srv, key, body, "merge")
	if w.Code != http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte(`"heartbeats"`)) {
		t.Fatalf("skipped heartbeat returned a second enrollment: %d %s", w.Code, w.Body.String())
	}
	if _, err := srv.store.GetHeartbeatByToken(context.Background(), token); err != nil {
		t.Fatal("merge changed existing heartbeat credential")
	}
}

func TestConfigurationHTTPMalformedInputDoesNotWrite(t *testing.T) {
	srv, key := testServer(t)
	t.Cleanup(srv.Close)
	for _, body := range []string{`{"version":2}{"version":2}`, `{"version":2,"Version":2}`, `{"version":2,"monitors":[null]}`} {
		if w := transferRequest(srv, key, []byte(body), "merge"); w.Code != http.StatusBadRequest {
			t.Fatalf("malformed input=%d %s", w.Code, w.Body.String())
		}
	}
	if got := getExport(t, srv, key, ""); len(got.Monitors) != 0 {
		t.Fatal("malformed import wrote configuration")
	}
}

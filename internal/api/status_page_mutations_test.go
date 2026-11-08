package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestStatusPageMutationRequiresMonitorReadAndAnalyticsTrustAPI(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) {
		cfg.Auth.APIKeys = append(cfg.Auth.APIKeys,
			config.APIKeyConfig{Name: "page-editor", Hash: config.HashAPIKey("editor"), Permissions: []string{"status_pages.read", "status_pages.create", "status_pages.update"}},
			config.APIKeyConfig{Name: "publisher", Hash: config.HashAPIKey("publisher"), Permissions: []string{"status_pages.read", "status_pages.create", "status_pages.update", "monitors.read", "incidents.read"}})
	}, false, nil)
	ctx := context.Background()
	m := &storage.Monitor{Name: "private monitor", Type: "http", Target: "https://private.example.test", Interval: 60, Timeout: 10}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	editor := map[string]string{"X-API-Key": "editor"}
	publisher := map[string]string{"X-API-Key": "publisher"}
	body := fmt.Sprintf(`{"title":"Services","slug":"services","monitors":[{"monitor_id":%d}]}`, m.ID)
	if w := env.do("POST", "/api/v1/status-pages", body, editor); w.Code != http.StatusForbidden {
		t.Fatalf("published unreadable monitor: %d %s", w.Code, w.Body.String())
	}
	w := env.do("POST", "/api/v1/status-pages", body, publisher)
	if w.Code != http.StatusCreated {
		t.Fatalf("authorized publication: %d %s", w.Code, w.Body.String())
	}
	page, err := env.store.GetStatusPageBySlug(ctx, "services")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/status-pages/%d", page.ID)
	w = env.do("GET", path, "", editor)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"monitor_id"`) {
		t.Fatalf("page read exposed hidden member IDs: %d %s", w.Code, w.Body.String())
	}
	w = env.do("PUT", path, `{"title":"Renamed"}`, editor)
	if w.Code != http.StatusOK {
		t.Fatalf("metadata-only edit denied: %d %s", w.Code, w.Body.String())
	}
	members, err := env.store.ListStatusPageMonitors(ctx, page.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("omitted membership lost: %v %v", members, err)
	}
	for _, input := range []string{`{"enabled":true}`, `{"api_enabled":true}`, `{"slug":"other"}`, `{"monitors":[]}`, `{"analytics_script":"<script src=\"https://analytics.example/a.js\"></script>"}`} {
		w := env.do("PUT", path, input, editor)
		if w.Code != http.StatusForbidden {
			t.Fatalf("unauthorized expansion/change %s: %d %s", input, w.Code, w.Body.String())
		}
	}
	w = env.do("PUT", path, `{"enabled":true,"password":"secret"}`, publisher)
	if w.Code != http.StatusOK {
		t.Fatalf("authorized enable/password: %d %s", w.Code, w.Body.String())
	}
	w = env.do("PUT", path, `{"password":""}`, editor)
	if w.Code != http.StatusForbidden {
		t.Fatalf("password removal exposed hidden membership: %d %s", w.Code, w.Body.String())
	}
	w = env.do("PUT", path, `{"analytics_script":"<script src=\"https://analytics.example/a.js\"></script>"}`, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("trusted analytics update: %d %s", w.Code, w.Body.String())
	}
}

func TestStatusPageWriteOnlyResponseDoesNotGrantRead(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) {
		cfg.Auth.APIKeys = append(cfg.Auth.APIKeys, config.APIKeyConfig{Name: "writer", Hash: config.HashAPIKey("writer"), Permissions: []string{"status_pages.create", "status_pages.update"}})
	}, false, nil)
	headers := map[string]string{"X-API-Key": "writer"}
	w := env.do("POST", "/api/v1/status-pages", `{"title":"Page","slug":"write-only"}`, headers)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusCreated || len(result) != 1 || result["id"] == nil {
		t.Fatalf("write-only creation disclosed metadata: %d %s", w.Code, w.Body.String())
	}
	page, err := env.store.GetStatusPageBySlug(context.Background(), "write-only")
	if err != nil {
		t.Fatal(err)
	}
	w = env.do("PUT", fmt.Sprintf("/api/v1/status-pages/%d", page.ID), `{"description":"updated"}`, headers)
	result = nil
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(result) != 1 || result["id"] == nil {
		t.Fatalf("write-only update disclosed metadata: %d %s", w.Code, w.Body.String())
	}
}

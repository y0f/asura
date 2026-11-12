package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestPublicStatusPageNotFound(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/status-pages/999/public", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublicStatusPageDisabled(t *testing.T) {
	srv, adminKey := testServer(t)

	ctx := context.Background()
	sp := &storage.StatusPage{
		Title:      "Disabled Page",
		Slug:       "disabled",
		Enabled:    false,
		APIEnabled: false,
	}
	if err := srv.store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/v1/status-pages/1/public", nil)
	req.Header.Set("X-API-Key", adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for disabled page, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublicStatusPageEnabled(t *testing.T) {
	srv, adminKey := testServer(t)

	ctx := context.Background()

	mon := &storage.Monitor{
		Name:             "Public Monitor",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         30,
		Timeout:          5,
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Enabled:          true,
	}
	if err := srv.store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	sp := &storage.StatusPage{
		Title:      "Public Page",
		Slug:       "public",
		Enabled:    true,
		APIEnabled: true,
	}
	if err := srv.store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}

	if err := srv.store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{
		{PageID: sp.ID, MonitorID: mon.ID, SortOrder: 0},
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/v1/status-pages/1/public", nil)
	req.Header.Set("X-API-Key", adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if _, ok := resp["monitors"]; !ok {
		t.Error("expected 'monitors' key in response")
	}
	if _, ok := resp["overall_status"]; !ok {
		t.Error("expected 'overall_status' key in response")
	}
	if _, ok := resp["page"]; !ok {
		t.Error("expected 'page' key in response")
	}

	monitors, ok := resp["monitors"].([]any)
	if !ok {
		t.Fatal("monitors should be an array")
	}
	if len(monitors) != 1 {
		t.Errorf("expected 1 monitor, got %d", len(monitors))
	}
}

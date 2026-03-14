package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "asura-server-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	store, err := storage.NewSQLiteStore(tmpFile.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	adminKey := "test-admin-key"
	readKey := "test-read-key"

	cfg := config.Defaults()
	cfg.Auth.APIKeys = []config.APIKeyConfig{
		{Name: "admin", Hash: config.HashAPIKey(adminKey), SuperAdmin: true},
		{Name: "reader", Hash: config.HashAPIKey(readKey), Permissions: []string{
			"monitors.read", "incidents.read", "notifications.read", "maintenance.read", "metrics.read",
		}},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	dispatcher := notifier.NewDispatcher(store, logger, true)
	srv := NewServer(cfg, store, nil, dispatcher, nil, logger, "test")

	return srv, adminKey
}

func TestHealthEndpoint(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/monitors", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthInvalidKey(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/monitors", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestListMonitorsEmpty(t *testing.T) {
	srv, adminKey := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/monitors", nil)
	req.Header.Set("X-API-Key", adminKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMonitorCRUD(t *testing.T) {
	srv, adminKey := testServer(t)

	body, _ := json.Marshal(map[string]any{
		"name":     "Test HTTP Monitor",
		"type":     "http",
		"target":   "https://example.com",
		"interval": 30,
		"timeout":  5,
	})

	req := httptest.NewRequest("POST", "/api/v1/monitors", bytes.NewReader(body))
	req.Header.Set("X-API-Key", adminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created storage.Monitor
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	req = httptest.NewRequest("GET", "/api/v1/monitors/1", nil)
	req.Header.Set("X-API-Key", adminKey)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("DELETE", "/api/v1/monitors/1", nil)
	req.Header.Set("X-API-Key", adminKey)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSecureHeaders(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	expected := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}

	for k, v := range expected {
		if got := w.Header().Get(k); got != v {
			t.Fatalf("header %s: expected %q, got %q", k, v, got)
		}
	}
}

func TestRequestID(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if id := w.Header().Get("X-Request-ID"); id == "" {
		t.Fatal("expected X-Request-ID header")
	}
}

func TestReadOnlyKeyCannotWrite(t *testing.T) {
	srv, _ := testServer(t)
	readKey := "test-read-key"

	body, _ := json.Marshal(map[string]any{
		"name":   "Test",
		"type":   "http",
		"target": "https://example.com",
	})

	req := httptest.NewRequest("POST", "/api/v1/monitors", bytes.NewReader(body))
	req.Header.Set("X-API-Key", readKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

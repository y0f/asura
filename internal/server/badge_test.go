package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func assertBadgeResponse(t *testing.T, w *httptest.ResponseRecorder, status int, value string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("badge status = %d, want %d: %s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(w.Body.String(), "<svg") || !strings.Contains(w.Body.String(), ">"+value+"</text>") {
		t.Fatalf("badge lost its SVG value: %v %s", w.Header(), w.Body.String())
	}
	cache := "max-age=300"
	if status != http.StatusOK {
		cache = "no-store"
	}
	if w.Header().Get("Cache-Control") != cache {
		t.Fatalf("badge cache policy = %q, want %q", w.Header().Get("Cache-Control"), cache)
	}
}

func createMonitorOnStatusPage(t *testing.T, srv *Server, adminKey string) int64 {
	t.Helper()

	ctx := context.Background()

	mon := &storage.Monitor{
		Name:             "Badge Test",
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
		Title:   "Test Page",
		Slug:    "test",
		Enabled: true,
	}
	if err := srv.store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}

	if err := srv.store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{
		{PageID: sp.ID, MonitorID: mon.ID, SortOrder: 0},
	}); err != nil {
		t.Fatal(err)
	}

	return mon.ID
}

func TestBadgeStatus(t *testing.T) {
	srv, adminKey := testServer(t)

	t.Run("not found for missing monitor", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/badge/999/status", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assertBadgeResponse(t, w, http.StatusNotFound, "not found")
	})

	t.Run("returns SVG for visible monitor", func(t *testing.T) {
		id := createMonitorOnStatusPage(t, srv, adminKey)

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/badge/%d/status", id), nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assertBadgeResponse(t, w, http.StatusOK, "pending")
	})

	t.Run("not found for monitor not on status page", func(t *testing.T) {
		ctx := context.Background()
		mon := &storage.Monitor{
			Name:             "Hidden Monitor",
			Type:             "tcp",
			Target:           "example.com:443",
			Interval:         30,
			Timeout:          5,
			FailureThreshold: 3,
			SuccessThreshold: 1,
			Enabled:          true,
		}
		if err := srv.store.CreateMonitor(ctx, mon); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/badge/%d/status", mon.ID), nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assertBadgeResponse(t, w, http.StatusNotFound, "not found")
		if strings.Contains(w.Body.String(), mon.Name) || strings.Contains(w.Body.String(), mon.Target) {
			t.Error("unpublished monitor metadata leaked in badge")
		}
	})
}

func TestBadgeUptime(t *testing.T) {
	srv, adminKey := testServer(t)
	createMonitorOnStatusPage(t, srv, adminKey)

	req := httptest.NewRequest("GET", "/api/v1/badge/1/uptime", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assertBadgeResponse(t, w, http.StatusOK, "no checks")
}

func TestBadgeResponseTime(t *testing.T) {
	srv, adminKey := testServer(t)
	createMonitorOnStatusPage(t, srv, adminKey)

	req := httptest.NewRequest("GET", "/api/v1/badge/1/response", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assertBadgeResponse(t, w, http.StatusOK, "no samples")
}

func TestBadgeInvalidID(t *testing.T) {
	srv, _ := testServer(t)

	for _, path := range []string{"/api/v1/badge/abc/status", "/api/v1/badge/abc/uptime", "/api/v1/badge/abc/response", "/api/v1/badge/abc/cert"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			assertBadgeResponse(t, w, http.StatusBadRequest, "error")
		})
	}
}

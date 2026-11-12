package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func seedMonitors(t *testing.T, srv *Server, n int) []int64 {
	t.Helper()
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	var ids []int64
	for i := 0; i < n; i++ {
		m := &storage.Monitor{
			Name: "Mon" + string(rune('A'+i)), Type: "http",
			Target: "https://example.com", Interval: 60, Timeout: 10,
			Enabled: true, FailureThreshold: 3, SuccessThreshold: 1,
		}
		if err := srv.store.CreateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	return ids
}

func bulkRequest(t *testing.T, srv *Server, key string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/monitors/bulk", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestBulkPause(t *testing.T) {
	srv, key := testServer(t)
	ids := seedMonitors(t, srv, 3)

	w := bulkRequest(t, srv, key, map[string]any{
		"action": "pause", "ids": ids[:2],
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["affected"] != float64(2) {
		t.Fatalf("expected 2 affected, got %v", resp["affected"])
	}

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	m1, _ := srv.store.GetMonitor(ctx, ids[0])
	m3, _ := srv.store.GetMonitor(ctx, ids[2])
	if m1.Enabled {
		t.Error("m1 should be paused")
	}
	if !m3.Enabled {
		t.Error("m3 should still be enabled")
	}
}

func TestBulkResume(t *testing.T) {
	srv, key := testServer(t)
	ids := seedMonitors(t, srv, 2)

	bulkRequest(t, srv, key, map[string]any{"action": "pause", "ids": ids})

	w := bulkRequest(t, srv, key, map[string]any{"action": "resume", "ids": ids})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	m1, _ := srv.store.GetMonitor(ctx, ids[0])
	if !m1.Enabled {
		t.Error("m1 should be resumed")
	}
}

func TestBulkDelete(t *testing.T) {
	srv, key := testServer(t)
	ids := seedMonitors(t, srv, 3)

	w := bulkRequest(t, srv, key, map[string]any{
		"action": "delete", "ids": []int64{ids[0], ids[2]},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	_, err := srv.store.GetMonitor(ctx, ids[0])
	if err == nil {
		t.Error("m1 should be deleted")
	}
	m2, err := srv.store.GetMonitor(ctx, ids[1])
	if err != nil {
		t.Fatal("m2 should still exist")
	}
	if m2.Name != "MonB" {
		t.Error("m2 name mismatch")
	}
}

func TestBulkSetGroup(t *testing.T) {
	srv, key := testServer(t)
	ids := seedMonitors(t, srv, 2)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	g := &storage.MonitorGroup{Name: "TestGroup"}
	srv.store.CreateMonitorGroup(ctx, g)

	w := bulkRequest(t, srv, key, map[string]any{
		"action": "set_group", "ids": ids, "group_id": g.ID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	m1, _ := srv.store.GetMonitor(ctx, ids[0])
	if m1.GroupID == nil || *m1.GroupID != g.ID {
		t.Error("m1 should be in group")
	}
}

func TestBulkInvalidAction(t *testing.T) {
	srv, key := testServer(t)
	w := bulkRequest(t, srv, key, map[string]any{
		"action": "nope", "ids": []int64{1},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestBulkEmptyIDs(t *testing.T) {
	srv, key := testServer(t)
	w := bulkRequest(t, srv, key, map[string]any{
		"action": "pause", "ids": []int64{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestBulkRequiresAuth(t *testing.T) {
	srv, _ := testServer(t)
	w := bulkRequest(t, srv, "", map[string]any{
		"action": "pause", "ids": []int64{1},
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

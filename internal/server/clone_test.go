package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func cloneRequest(t *testing.T, srv *Server, key string, id int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/monitors/%d/clone", id), nil)
	req.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestCloneMonitor(t *testing.T) {
	srv, key := testServer(t)
	ids := seedMonitors(t, srv, 1)

	w := cloneRequest(t, srv, key, ids[0])
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var clone storage.Monitor
	json.NewDecoder(w.Body).Decode(&clone)
	if clone.ID == ids[0] {
		t.Error("clone should have a different ID")
	}
	if clone.Name != "MonA (copy)" {
		t.Errorf("expected name 'MonA (copy)', got %q", clone.Name)
	}
	if clone.Enabled {
		t.Error("clone should be disabled")
	}
}

func TestCloneMonitorCopiesFields(t *testing.T) {
	srv, key := testServer(t)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	gid := int64(0)
	g := &storage.MonitorGroup{Name: "G1"}
	srv.store.CreateMonitorGroup(ctx, g)
	gid = g.ID

	m := &storage.Monitor{
		Name: "Source", Type: "http", Target: "https://example.com",
		Interval: 30, Timeout: 5, Enabled: true,
		FailureThreshold: 5, SuccessThreshold: 2,
		Tags: []string{"prod", "api"}, UpsideDown: true,
		ResendInterval: 120, GroupID: &gid,
	}
	srv.store.CreateMonitor(ctx, m)

	w := cloneRequest(t, srv, key, m.ID)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var clone storage.Monitor
	json.NewDecoder(w.Body).Decode(&clone)

	if clone.Type != "http" {
		t.Errorf("type: got %q", clone.Type)
	}
	if clone.Target != "https://example.com" {
		t.Errorf("target: got %q", clone.Target)
	}
	if clone.Interval != 30 {
		t.Errorf("interval: got %d", clone.Interval)
	}
	if clone.FailureThreshold != 5 {
		t.Errorf("failure_threshold: got %d", clone.FailureThreshold)
	}
	if !clone.UpsideDown {
		t.Error("upside_down should be true")
	}
	if clone.ResendInterval != 120 {
		t.Errorf("resend_interval: got %d", clone.ResendInterval)
	}
	if clone.GroupID == nil || *clone.GroupID != gid {
		t.Error("group_id should be copied")
	}
}

func TestCloneMonitorNotFound(t *testing.T) {
	srv, key := testServer(t)
	w := cloneRequest(t, srv, key, 999)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCloneMonitorCopiesNotificationChannels(t *testing.T) {
	srv, key := testServer(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	m := &storage.Monitor{
		Name: "Source", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	srv.store.CreateMonitor(ctx, m)

	ch := &storage.NotificationChannel{
		Name: "TestCh", Type: "webhook",
		Settings: json.RawMessage(`{"url":"https://hook.example.com"}`),
	}
	srv.store.CreateNotificationChannel(ctx, ch)
	srv.store.SetMonitorNotificationChannels(ctx, m.ID, []int64{ch.ID})

	w := cloneRequest(t, srv, key, m.ID)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var clone storage.Monitor
	json.NewDecoder(w.Body).Decode(&clone)

	chIDs, _ := srv.store.GetMonitorNotificationChannelIDs(ctx, clone.ID)
	if len(chIDs) != 1 || chIDs[0] != ch.ID {
		t.Errorf("expected notification channels to be copied, got %v", chIDs)
	}
}

func TestCloneRequiresAuth(t *testing.T) {
	srv, _ := testServer(t)
	w := cloneRequest(t, srv, "", 1)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

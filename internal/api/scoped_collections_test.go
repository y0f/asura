package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/api"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func collectionFixture(t *testing.T) (*api.Handler, storage.Store, []*storage.MonitorGroup, []*storage.Monitor) {
	t.Helper()
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	groups := []*storage.MonitorGroup{{Name: "allowed"}, {Name: "hidden"}}
	monitors := make([]*storage.Monitor, 2)
	for i, g := range groups {
		if err := env.store.CreateMonitorGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
		monitors[i] = &storage.Monitor{Name: fmt.Sprintf("private-monitor-%d", i), Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10, Enabled: true}
		if err := env.store.CreateMonitor(ctx, monitors[i]); err != nil {
			t.Fatal(err)
		}
	}
	h := api.New(config.Defaults(), env.store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	return h, env.store, groups, monitors
}

func TestHistoryRequestsFilterCountsRowsStatsAndRelatedMetadata(t *testing.T) {
	h, store, groups, monitors := collectionFixture(t)
	ctx := context.Background()
	channel := &storage.NotificationChannel{Name: "private-channel", Type: "webhook", Settings: json.RawMessage(`{"url":"https://example.test"}`), Enabled: true}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	for i, owner := range []*int64{&monitors[0].ID, &monitors[1].ID, nil} {
		if err := store.InsertNotificationHistory(ctx, &storage.NotificationHistory{ChannelID: channel.ID, MonitorID: owner, EventType: "incident.created", Status: "failed", Error: "https://user:private-credential@example.test"}); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertRequestLogBatch(ctx, []*storage.RequestLog{{MonitorID: owner, Path: fmt.Sprintf("/owner-%d", i), Method: "GET", StatusCode: 200, ClientIP: fmt.Sprintf("192.0.2.%d", i+1), CreatedAt: time.Now().UTC().Add(-time.Minute)}}); err != nil {
			t.Fatal(err)
		}
	}
	do := func(handler http.HandlerFunc, action, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, incidentPermissionKey(t, action, groups[0].ID)))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	w := do(h.ListNotificationHistory, "notification_history.read", "/api/v1/notifications/history?per_page=1")
	var history struct {
		Total int64                          `json:"total"`
		Data  []*storage.NotificationHistory `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil || w.Code != 200 || history.Total != 1 || len(history.Data) != 1 || *history.Data[0].MonitorID != monitors[0].ID {
		t.Fatalf("history scope/count failure: %d %s %v", w.Code, w.Body, err)
	}
	if strings.Contains(w.Body.String(), "private-") || history.Data[0].ChannelType != "" || history.Data[0].Error != "Delivery failed" {
		t.Fatalf("history leaked related name/provider secret: %s", w.Body)
	}
	w = do(h.ListRequestLogs, "request_logs.read", "/api/v1/logs?per_page=1")
	var logs struct {
		Total int64                 `json:"total"`
		Data  []*storage.RequestLog `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &logs); err != nil || w.Code != 200 || logs.Total != 1 || len(logs.Data) != 1 || logs.Data[0].Path != "/owner-0" {
		t.Fatalf("log scope/count failure: %d %s %v", w.Code, w.Body, err)
	}
	w = do(h.RequestLogStats, "request_logs.read", "/api/v1/logs/stats")
	var stats storage.RequestLogStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil || w.Code != 200 || stats.TotalRequests != 1 || stats.UniqueVisitors != 1 || len(stats.TopPaths) != 1 || stats.TopPaths[0].Path != "/owner-0" {
		t.Fatalf("stats leaked other owners: %d %s %v", w.Code, w.Body, err)
	}
	// Scope follows current ownership, including aggregate queries after a move.
	monitors[0].GroupID = &groups[1].ID
	if err := store.UpdateMonitor(ctx, monitors[0]); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		handler      http.HandlerFunc
		action, path string
	}{{h.ListNotificationHistory, "notification_history.read", "/history"}, {h.ListRequestLogs, "request_logs.read", "/logs"}, {h.RequestLogStats, "request_logs.read", "/stats"}} {
		w = do(tc.handler, tc.action, tc.path)
		if w.Code != 200 || strings.Contains(w.Body.String(), "/owner-0") || strings.Contains(w.Body.String(), "192.0.2.1") || strings.Contains(w.Body.String(), `"total":1`) || strings.Contains(w.Body.String(), `"total_requests":1`) {
			t.Fatalf("revoked history remains visible: %d %s", w.Code, w.Body)
		}
	}
}

func TestMaintenanceRequestsRequireFullTargetAndActionCoverage(t *testing.T) {
	h, store, groups, monitors := collectionFixture(t)
	ctx := context.Background()
	windows := []*storage.MaintenanceWindow{
		{Name: "allowed-window", MonitorIDs: []int64{monitors[0].ID}, Recurring: "manual", Active: true},
		{Name: "hidden-mixed", MonitorIDs: []int64{monitors[0].ID, monitors[1].ID}, Recurring: "manual"},
		{Name: "hidden-global", Recurring: "manual"},
	}
	for _, window := range windows {
		if err := store.CreateMaintenanceWindow(ctx, window); err != nil {
			t.Fatal(err)
		}
	}
	do := func(handler http.HandlerFunc, action, method string, id int64, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/maintenance?per_page=1", strings.NewReader(body))
		r.SetPathValue("id", fmt.Sprint(id))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, incidentPermissionKey(t, action, groups[0].ID)))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	body := `{"name":"metadata","recurring":"manual"}`
	w := do(h.ListMaintenance, "maintenance.read", "GET", 0, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "allowed-window") || strings.Contains(w.Body.String(), "hidden-") {
		t.Fatalf("maintenance list accepted partial coverage: %d %s", w.Code, w.Body)
	}
	for _, window := range windows[1:] {
		for _, tc := range []struct {
			handler              http.HandlerFunc
			action, method, body string
		}{{h.UpdateMaintenance, "maintenance.update", "PUT", body}, {h.ToggleMaintenance, "maintenance.toggle", "POST", ""}, {h.DeleteMaintenance, "maintenance.delete", "DELETE", ""}} {
			w = do(tc.handler, tc.action, tc.method, window.ID, tc.body)
			if w.Code != 404 {
				t.Fatalf("partial %s admitted window%d: %d %s", tc.action, window.ID, w.Code, w.Body)
			}
		}
	}
	w = do(h.UpdateMaintenance, "maintenance.update", "PUT", windows[0].ID, body)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != fmt.Sprintf(`{"id":%d}`, windows[0].ID) {
		t.Fatalf("write-only edit leaked data: %d %s", w.Code, w.Body)
	}
	stored, err := store.GetMaintenanceWindow(ctx, windows[0].ID)
	if err != nil || !stored.Active || len(stored.MonitorIDs) != 1 || stored.MonitorIDs[0] != monitors[0].ID {
		t.Fatalf("omitted state/targets broadened edit: %+v %v", stored, err)
	}
	w = do(h.UpdateMaintenance, "maintenance.update", "PUT", windows[0].ID, `{"name":"denied","recurring":"manual","active":false}`)
	if w.Code != 404 {
		t.Fatalf("update authority implied toggle: %d %s", w.Code, w.Body)
	}
	for _, ids := range []string{fmt.Sprintf("[%d,%d]", monitors[0].ID, monitors[1].ID), "[]"} {
		w = do(h.CreateMaintenance, "maintenance.create", "POST", 0, `{"name":"forbidden","recurring":"manual","monitor_ids":`+ids+`}`)
		if w.Code != 404 {
			t.Fatalf("create broadened scope: %d %s", w.Code, w.Body)
		}
	}
	w = do(h.CreateMaintenance, "maintenance.create", "POST", 0, fmt.Sprintf(`{"name":"new","recurring":"manual","monitor_ids":[%d]}`, monitors[0].ID))
	var created map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != 201 || len(created) != 1 || created["id"] == nil {
		t.Fatalf("write-only creation response: %d %s %v", w.Code, w.Body, err)
	}
}

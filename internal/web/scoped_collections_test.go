package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

func scopedCollectionKey(t *testing.T, groupID int64, actions ...string) *config.APIKeyConfig {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.Roles = []config.RoleConfig{{Name: "scoped", Permissions: actions}}
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "operator", Hash: config.HashAPIKey("operator"), RoleBindings: []config.RoleBindingConfig{{Role: "scoped", Scope: config.MonitorAccess{GroupIDs: []int64{groupID}}}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return &cfg.Auth.APIKeys[0]
}

func webCollectionFixture(t *testing.T) (*Handler, *storage.SQLiteStore, []*storage.MonitorGroup, []*storage.Monitor) {
	t.Helper()
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	ctx := context.Background()
	groups := []*storage.MonitorGroup{{Name: "allowed"}, {Name: "hidden"}}
	monitors := make([]*storage.Monitor, 2)
	for i, g := range groups {
		if err := store.CreateMonitorGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
		monitors[i] = &storage.Monitor{Name: fmt.Sprintf("private-monitor-%d", i), Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10, Enabled: true}
		if err := store.CreateMonitor(ctx, monitors[i]); err != nil {
			t.Fatal(err)
		}
	}
	return h, store, groups, monitors
}

func TestScopedWebCollectionsKeepMetadataChoicesAndDashboardSeparate(t *testing.T) {
	h, store, groups, monitors := webCollectionFixture(t)
	ctx := context.Background()
	channel := &storage.NotificationChannel{Name: "private-channel", Type: "webhook", Settings: json.RawMessage(`{"url":"https://example.test"}`), Enabled: true}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	for i, owner := range []*int64{&monitors[0].ID, &monitors[1].ID, nil} {
		if err := store.InsertNotificationHistory(ctx, &storage.NotificationHistory{ChannelID: channel.ID, MonitorID: owner, EventType: "incident.created", Status: "failed", Error: "https://user:private-credential@example.test"}); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertRequestLogBatch(ctx, []*storage.RequestLog{{MonitorID: owner, Path: fmt.Sprintf("/owner-%d", i), ClientIP: fmt.Sprintf("192.0.2.%d", i+1), Method: "GET", StatusCode: 200, CreatedAt: time.Now().UTC().Add(-time.Minute)}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range monitors {
		if _, _, err := store.RecordIncidentFailure(ctx, m.ID, "warning", fmt.Sprintf("incident-cause-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	do := func(handler http.HandlerFunc, actions ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/console/", nil)
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, scopedCollectionKey(t, groups[0].ID, actions...)))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	w := do(h.NotificationHistory, "notification_history.read")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-") || !strings.Contains(w.Body.String(), "Delivery failed") {
		t.Fatalf("history leaked names/provider response: %d %s", w.Code, w.Body)
	}
	w = do(h.RequestLogs, "request_logs.read")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/owner-0") || strings.Contains(w.Body.String(), "/owner-1") || strings.Contains(w.Body.String(), "/owner-2") || strings.Contains(w.Body.String(), "192.0.2.2") || strings.Contains(w.Body.String(), "192.0.2.3") {
		t.Fatalf("log rows/stats/IP choices leaked owners: %d %s", w.Code, w.Body)
	}
	w = do(h.Dashboard, "monitors.read")
	if w.Code != 200 || !strings.Contains(w.Body.String(), monitors[0].Name) || strings.Contains(w.Body.String(), monitors[1].Name) || strings.Contains(w.Body.String(), "incident-cause-") {
		t.Fatalf("dashboard mixed resource scopes: %d %s", w.Code, w.Body)
	}
	w = do(h.Dashboard, "monitors.read", "incidents.read")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "incident-cause-0") || strings.Contains(w.Body.String(), "incident-cause-1") {
		t.Fatalf("dashboard incident scope failed: %d %s", w.Code, w.Body)
	}
	// An incident role alone must never gain monitor names from dashboard cards.
	w = do(h.Dashboard, "incidents.read")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-monitor-") {
		t.Fatalf("incident capability implied monitor read: %d %s", w.Code, w.Body)
	}
}

func TestScopedMaintenanceWebActionsChoicesAndWriteOnlyRedirect(t *testing.T) {
	h, store, groups, monitors := webCollectionFixture(t)
	ctx := context.Background()
	allowed := &storage.MaintenanceWindow{Name: "allowed-window", MonitorIDs: []int64{monitors[0].ID}, Recurring: "manual", Active: true}
	mixed := &storage.MaintenanceWindow{Name: "hidden-mixed-window", MonitorIDs: []int64{monitors[0].ID, monitors[1].ID}, Recurring: "manual"}
	for _, window := range []*storage.MaintenanceWindow{allowed, mixed} {
		if err := store.CreateMaintenanceWindow(ctx, window); err != nil {
			t.Fatal(err)
		}
	}
	do := func(handler http.HandlerFunc, id int64, form url.Values, actions ...string) *httptest.ResponseRecorder {
		method := "GET"
		if form != nil {
			method = "POST"
		}
		r := adminRequest(method, "/console/maintenance", form)
		r.SetPathValue("id", fmt.Sprint(id))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, scopedCollectionKey(t, groups[0].ID, actions...)))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	w := do(h.Maintenance, 0, nil, "maintenance.read", "maintenance.create", "maintenance.toggle")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, allowed.Name) || strings.Contains(body, mixed.Name) || strings.Contains(body, "private-monitor-") || !strings.Contains(body, fmt.Sprintf("Monitor #%d", monitors[0].ID)) || strings.Contains(body, fmt.Sprintf("Monitor #%d", monitors[1].ID)) || !strings.Contains(body, fmt.Sprintf("/console/maintenance/%d/toggle", allowed.ID)) {
		t.Fatalf("window/choice/action coverage failed: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		handler http.HandlerFunc
		action  string
	}{{h.MaintenanceDelete, "maintenance.delete"}, {h.MaintenanceToggle, "maintenance.toggle"}} {
		w = do(tc.handler, mixed.ID, url.Values{}, tc.action)
		if w.Code != 404 || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("partial %s reported success: %d %v", tc.action, w.Code, w.Header())
		}
	}
	w = do(h.MaintenanceUpdate, allowed.ID, url.Values{"name": {"renamed"}, "recurring": {"manual"}}, "maintenance.update", "notification_history.read")
	if w.Code != 303 || w.Header().Get("Location") != "/console/notifications/history" {
		t.Fatalf("write-only edit redirected to unreadable page: %d %v", w.Code, w.Header())
	}
	stored, err := store.GetMaintenanceWindow(ctx, allowed.ID)
	if err != nil || !stored.Active || len(stored.MonitorIDs) != 1 || stored.MonitorIDs[0] != monitors[0].ID {
		t.Fatalf("omitted current state lost: %+v %v", stored, err)
	}
	w = do(h.MaintenanceUpdate, allowed.ID, url.Values{"name": {"forbidden"}, "recurring": {"manual"}, "active": {"off"}}, "maintenance.update")
	if w.Code != 404 {
		t.Fatalf("update implied active-state authority: %d %s", w.Code, w.Body)
	}
	w = do(h.MaintenanceCreate, 0, url.Values{"name": {"all forbidden"}, "recurring": {"manual"}}, "maintenance.create")
	if w.Code != 404 {
		t.Fatalf("scoped empty selection became all monitors: %d %s", w.Code, w.Body)
	}
}

type failingCollectionStore struct {
	storage.Store
	fail string
}

func (s failingCollectionStore) ListIncidentsFiltered(ctx context.Context, f storage.IncidentListFilter, p storage.Pagination) (*storage.PaginatedResult, error) {
	if s.fail == "incidents" {
		return nil, errors.New("injected query failure")
	}
	return s.Store.ListIncidentsFiltered(ctx, f, p)
}
func (s failingCollectionStore) ListNotificationHistory(ctx context.Context, f storage.NotifHistoryFilter, p storage.Pagination) (*storage.PaginatedResult, error) {
	if s.fail == "history" {
		return nil, errors.New("injected query failure")
	}
	return s.Store.ListNotificationHistory(ctx, f, p)
}
func (s failingCollectionStore) GetRequestLogStats(ctx context.Context, from, to time.Time, access ...storage.MonitorAccess) (*storage.RequestLogStats, error) {
	if s.fail == "stats" {
		return nil, errors.New("injected query failure")
	}
	return s.Store.GetRequestLogStats(ctx, from, to, access...)
}
func (s failingCollectionStore) ListMaintenanceWindows(ctx context.Context, access ...storage.MonitorAccess) ([]*storage.MaintenanceWindow, error) {
	if s.fail == "maintenance" {
		return nil, errors.New("injected query failure")
	}
	return s.Store.ListMaintenanceWindows(ctx, access...)
}

func TestScopedCollectionFailuresDoNotRenderHealthyEmptyPages(t *testing.T) {
	h, store, groups, _ := webCollectionFixture(t)
	for _, tc := range []struct {
		fail, action string
		handler      http.HandlerFunc
	}{{"incidents", "incidents.read", h.Dashboard}, {"history", "notification_history.read", h.NotificationHistory}, {"stats", "request_logs.read", h.RequestLogs}, {"maintenance", "maintenance.read", h.Maintenance}} {
		h.store = failingCollectionStore{Store: store, fail: tc.fail}
		r := httptest.NewRequest("GET", "/console/", nil)
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, scopedCollectionKey(t, groups[0].ID, "monitors.read", tc.action)))
		w := httptest.NewRecorder()
		tc.handler(w, r)
		if w.Code != 500 || strings.Contains(w.Body.String(), "<!doctype") {
			t.Fatalf("%s failure rendered success: %d %s", tc.fail, w.Code, w.Body)
		}
	}
	h.store = failingCollectionStore{Store: store, fail: "incidents"}
	r := httptest.NewRequest("GET", "/console/", nil)
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, scopedCollectionKey(t, groups[0].ID, "monitors.read")))
	w := httptest.NewRecorder()
	h.Dashboard(w, r)
	if w.Code != 200 {
		t.Fatalf("dashboard queried incidents without capability: %d %s", w.Code, w.Body)
	}
}

type heldMaintenanceFlipStore struct {
	storage.Store
	entered, resume chan struct{}
}

func (s heldMaintenanceFlipStore) FlipMaintenanceWindow(ctx context.Context, id int64, access []storage.MonitorAccess) (*storage.MaintenanceWindow, error) {
	close(s.entered)
	select {
	case <-s.resume:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Store.FlipMaintenanceWindow(ctx, id, access)
}
func (s heldMaintenanceFlipStore) GetMaintenanceWindow(context.Context, int64, ...storage.MonitorAccess) (*storage.MaintenanceWindow, error) {
	panic("toggle must not pre-read maintenance state")
}

type maintenanceCaptureSender struct{ received chan *notifier.Payload }

func (maintenanceCaptureSender) Type() string { return "maintenance-capture" }
func (s maintenanceCaptureSender) Send(ctx context.Context, _ *storage.NotificationChannel, p *notifier.Payload) error {
	select {
	case s.received <- p:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestMaintenanceToggleNotificationUsesCommittedCurrentWindow(t *testing.T) {
	h, store, _, monitors := webCollectionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := &storage.MaintenanceWindow{Name: "old-window", Recurring: "manual", MonitorIDs: []int64{monitors[0].ID}}
	if err := store.CreateMaintenanceWindow(ctx, window); err != nil {
		t.Fatal(err)
	}
	channel := &storage.NotificationChannel{Name: "capture", Type: "maintenance-capture", Settings: json.RawMessage(`{}`), Enabled: true}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	sender := maintenanceCaptureSender{received: make(chan *notifier.Payload, 1)}
	h.notifier = notifier.NewDispatcher(store, h.logger, true)
	h.notifier.RegisterSender(sender)
	defer func() {
		stop, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := h.notifier.Shutdown(stop); err != nil {
			t.Error(err)
		}
	}()
	held := heldMaintenanceFlipStore{Store: store, entered: make(chan struct{}), resume: make(chan struct{})}
	h.store = held
	request := adminRequest("POST", "/console/maintenance/1/toggle", url.Values{})
	request.SetPathValue("id", fmt.Sprint(window.ID))
	key := &config.APIKeyConfig{Name: "operator", Permissions: []string{"maintenance.toggle", "notification_history.read"}}
	request = request.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, key))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.MaintenanceToggle(response, request) }()
	select {
	case <-held.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	window.Name = "current-window"
	window.MonitorIDs = []int64{monitors[1].ID}
	window.Active = true
	err := store.UpdateMaintenanceWindow(ctx, window)
	close(held.resume)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != 303 || response.Header().Get("Location") != "/console/notifications/history" {
		t.Fatalf("toggle response: %d %v", response.Code, response.Header())
	}
	select {
	case payload := <-sender.received:
		if payload.EventType != "maintenance.ended" || payload.Monitor == nil || !strings.Contains(payload.Monitor.Name, "current-window") || strings.Contains(payload.Monitor.Name, "old-window") {
			t.Fatalf("notification used stale row: %+v", payload)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stored, err := store.GetMaintenanceWindow(ctx, window.ID)
	if err != nil || stored.Active || len(stored.MonitorIDs) != 1 || stored.MonitorIDs[0] != monitors[1].ID {
		t.Fatalf("flip overwrote current targets/state: %+v %v", stored, err)
	}
}

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

func incidentPermissionKey(t *testing.T, action string, groupID int64) *config.APIKeyConfig {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.Roles = []config.RoleConfig{{Name: "scoped", Permissions: []string{action}}}
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "operator", Hash: config.HashAPIKey("operator"), RoleBindings: []config.RoleBindingConfig{{Role: "scoped", Scope: config.MonitorAccess{GroupIDs: []int64{groupID}}}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return &cfg.Auth.APIKeys[0]
}

func TestIncidentRequestsKeepActionsAndRelatedReadsScoped(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	groups := []*storage.MonitorGroup{{Name: "allowed"}, {Name: "hidden"}}
	monitors := make([]*storage.Monitor, 2)
	incidents := make([]*storage.Incident, 2)
	for i, group := range groups {
		if err := env.store.CreateMonitorGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
		monitors[i] = &storage.Monitor{Name: fmt.Sprintf("private-monitor-%d", i), Type: "http", GroupID: &group.ID, Interval: 60, Timeout: 10}
		if err := env.store.CreateMonitor(ctx, monitors[i]); err != nil {
			t.Fatal(err)
		}
		var err error
		incidents[i], _, err = env.store.RecordIncidentFailure(ctx, monitors[i].ID, "warning", fmt.Sprintf("private-cause-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	h := api.New(config.Defaults(), env.store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	do := func(handler http.HandlerFunc, action, method string, id int64, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, fmt.Sprintf("/api/v1/incidents/%d?per_page=1", id), strings.NewReader(body))
		r.SetPathValue("id", fmt.Sprint(id))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, incidentPermissionKey(t, action, groups[0].ID)))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	w := do(h.ListIncidents, "incidents.read", "GET", 0, "")
	var list struct {
		Total int64               `json:"total"`
		Data  []*storage.Incident `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || list.Total != 1 || len(list.Data) != 1 || list.Data[0].ID != incidents[0].ID || list.Data[0].MonitorName != "" {
		t.Fatalf("list leaked counts or related name: %d %s, %v", w.Code, w.Body.String(), err)
	}
	w = do(h.GetIncident, "incidents.read", "GET", incidents[0].ID, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), monitors[0].Name) || strings.Contains(w.Body.String(), incidents[0].Cause) {
		t.Fatalf("detail related-resource boundary: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		action, method, body string
		handler              http.HandlerFunc
	}{
		{"incidents.read", "GET", "", h.GetIncident},
		{"incidents.acknowledge", "POST", "", h.AckIncident},
		{"incidents.resolve", "POST", "", h.ResolveIncident},
		{"incidents.severity", "POST", `{"severity":"critical"}`, h.SetIncidentSeverity},
		{"incidents.delete", "DELETE", "", h.DeleteIncident},
	} {
		w = do(tc.handler, tc.action, tc.method, incidents[1].ID, tc.body)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "private-") {
			t.Fatalf("%s admitted another group's incident: %d %s", tc.action, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		action, body string
		handler      http.HandlerFunc
	}{
		{"incidents.acknowledge", "", h.AckIncident},
		{"incidents.severity", `{"severity":"critical"}`, h.SetIncidentSeverity},
		{"incidents.resolve", "", h.ResolveIncident},
	} {
		w = do(tc.handler, tc.action, "POST", incidents[0].ID, tc.body)
		var response map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || len(response) != 1 || response["id"] == nil {
			t.Fatalf("write-only %s exposed incident data: %d %s, %v", tc.action, w.Code, w.Body.String(), err)
		}
	}
	w = do(h.ResolveIncident, "incidents.resolve", "POST", incidents[0].ID, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate resolution reported success: %d %s", w.Code, w.Body.String())
	}
	hidden, err := env.store.GetIncident(ctx, incidents[1].ID)
	if err != nil || hidden.Status != "open" || hidden.Severity != "warning" {
		t.Fatalf("denied mutations changed hidden incident: %+v, %v", hidden, err)
	}
}

type heldIncidentLookupStore struct {
	storage.Store
	read, resume chan struct{}
}

func (s heldIncidentLookupStore) GetIncident(ctx context.Context, id int64, access ...storage.MonitorAccess) (*storage.Incident, error) {
	inc, err := s.Store.GetIncident(ctx, id, access...)
	close(s.read)
	select {
	case <-s.resume:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return inc, err
}

func TestIncidentTimelineRechecksGroupAfterLookup(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := &storage.MonitorGroup{Name: "allowed"}
	if err := env.store.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "private-name", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	inc, _, err := env.store.RecordIncidentFailure(ctx, m.ID, "critical", "private-timeline")
	if err != nil {
		t.Fatal(err)
	}
	held := heldIncidentLookupStore{Store: env.store, read: make(chan struct{}), resume: make(chan struct{})}
	h := api.New(config.Defaults(), held, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	r := httptest.NewRequest("GET", "/api/v1/incidents/"+fmt.Sprint(inc.ID), nil)
	r.SetPathValue("id", fmt.Sprint(inc.ID))
	r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, incidentPermissionKey(t, "incidents.read", g.ID)))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.GetIncident(w, r) }()
	select {
	case <-held.read:
	case <-ctx.Done():
		t.Fatal("lookup not reached")
	}
	m.GroupID = nil
	err = env.store.UpdateMonitor(ctx, m)
	close(held.resume)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("request did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("timeline ignored current ownership: %d %s", w.Code, w.Body.String())
	}
}

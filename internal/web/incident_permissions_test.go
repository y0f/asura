package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestIncidentWebScopesAndWriteOnlyRedirect(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	ctx := context.Background()
	g := &storage.MonitorGroup{Name: "allowed"}
	if err := store.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "private-monitor-name", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	inc, _, err := store.RecordIncidentFailure(ctx, m.ID, "warning", "incident-visible-cause")
	if err != nil {
		t.Fatal(err)
	}
	key := func(action string, allowed bool) *config.APIKeyConfig {
		t.Helper()
		cfg := config.Defaults()
		id := g.ID
		if !allowed {
			id++
		}
		cfg.Auth.Roles = []config.RoleConfig{{Name: "scoped", Permissions: []string{action}}}
		cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "operator", Hash: config.HashAPIKey("operator"), RoleBindings: []config.RoleBindingConfig{{Role: "scoped", Scope: config.MonitorAccess{GroupIDs: []int64{id}}}}}}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		return &cfg.Auth.APIKeys[0]
	}
	do := func(handler http.HandlerFunc, action, method string, allowed bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/console/incidents/"+strconvI(inc.ID), nil)
		r.SetPathValue("id", strconvI(inc.ID))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, key(action, allowed)))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	for _, tc := range []struct {
		action, method string
		handler        http.HandlerFunc
	}{
		{"incidents.read", "GET", h.IncidentDetail},
		{"incidents.acknowledge", "POST", h.IncidentAck},
		{"incidents.resolve", "POST", h.IncidentResolve},
		{"incidents.delete", "POST", h.IncidentDelete},
	} {
		w := do(tc.handler, tc.action, tc.method, false)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "incident-visible-cause") || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("out-of-scope %s did not fail closed: %d %s", tc.action, w.Code, w.Body.String())
		}
	}
	w := do(h.Incidents, "incidents.read", "GET", false)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), inc.Cause) {
		t.Fatalf("incident list exposed denied object: %d %s", w.Code, w.Body.String())
	}
	w = do(h.IncidentDetail, "incidents.read", "GET", true)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), m.Name) || strings.Contains(w.Body.String(), "/console/monitors/"+strconvI(m.ID)) || strings.Contains(w.Body.String(), inc.Cause) {
		t.Fatalf("incident detail related-resource leak: %d %s", w.Code, w.Body.String())
	}
	w = do(h.IncidentAck, "incidents.acknowledge", "POST", true)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/console/" {
		t.Fatalf("write-only action redirected to unreadable incident: %d %v", w.Code, w.Header())
	}
	stored, err := store.GetIncident(ctx, inc.ID)
	if err != nil || stored.Status != "acknowledged" || stored.AcknowledgedBy != "operator" {
		t.Fatalf("authorized acknowledgement failed: %+v, %v", stored, err)
	}
}

type failedIncidentTransitionStore struct{ storage.Store }

func (failedIncidentTransitionStore) TransitionIncident(context.Context, int64, storage.IncidentTransition, ...storage.MonitorAccess) (*storage.Incident, error) {
	return nil, errors.New("injected timeline failure")
}

func TestIncidentWebPersistenceFailureDoesNotReportSuccess(t *testing.T) {
	h, store := storeHandler(t)
	h.store = failedIncidentTransitionStore{store}
	for _, handler := range []http.HandlerFunc{h.IncidentAck, h.IncidentResolve} {
		r := adminRequest("POST", "/incidents/1", nil)
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusInternalServerError || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" {
			t.Fatalf("failed transition reported success: %d %v", w.Code, w.Header())
		}
	}
}

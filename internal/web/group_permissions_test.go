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

func TestGroupDetailRequiresIndependentMonitorRead(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	g := &storage.MonitorGroup{Name: "known group"}
	if err := store.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "private monitor", Target: "https://private.example.test", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, canRead := range []bool{false, true} {
		key := &config.APIKeyConfig{Permissions: []string{"groups.read"}}
		if canRead {
			key.Permissions = append(key.Permissions, "monitors.read")
		}
		r := adminRequest("GET", "/groups/"+strconvI(g.ID), nil)
		r.SetPathValue("id", strconvI(g.ID))
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		h.GroupDetail(w, r)
		body := w.Body.String()
		if w.Code != http.StatusOK || strings.Contains(body, m.Name) != canRead || strings.Contains(body, m.Target) != canRead {
			t.Fatalf("independent monitor read %v: code=%d body=%s", canRead, w.Code, body)
		}
		if !canRead && (!strings.Contains(body, "0 visible monitors") || strings.Contains(body, "1 visible monitor")) {
			t.Fatal("group count disclosed hidden members")
		}
	}
	visible := &storage.Monitor{Name: "visible monitor", Target: "https://visible.example.test", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, visible); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Auth.Roles = []config.RoleConfig{{Name: "reader", Permissions: []string{"monitors.read"}}}
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "reader", Hash: config.HashAPIKey("test"), Permissions: []string{"groups.read"}, RoleBindings: []config.RoleBindingConfig{{Role: "reader", Scope: config.MonitorAccess{MonitorIDs: []int64{visible.ID}}}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := adminRequest("GET", "/groups/"+strconvI(g.ID), nil)
	r.SetPathValue("id", strconvI(g.ID))
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &cfg.Auth.APIKeys[0]))
	w := httptest.NewRecorder()
	h.GroupDetail(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, m.Name) || strings.Contains(body, m.Target) || !strings.Contains(body, visible.Name) || !strings.Contains(body, "1 visible monitor") {
		t.Fatalf("ID-scoped membership not filtered before rendering/count: %d %s", w.Code, body)
	}
}

type failingGroupReads struct{ storage.Store }

func (failingGroupReads) ListMonitorGroups(context.Context) ([]*storage.MonitorGroup, error) {
	return nil, errors.New("injected read failure")
}

func TestGroupFailuresAreNotEmptyOrSuccessful(t *testing.T) {
	h, store := storeHandler(t)
	h.store = failingGroupReads{store}
	w := httptest.NewRecorder()
	h.Groups(w, adminRequest("GET", "/groups", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "No groups") {
		t.Fatalf("read failure became empty state: %d", w.Code)
	}
	h.store = store
	g := &storage.MonitorGroup{Name: "occupied"}
	if err := store.CreateMonitorGroup(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "member", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	r := adminRequest("POST", "/groups/"+strconvI(g.ID)+"/delete", nil)
	r.SetPathValue("id", strconvI(g.ID))
	w = httptest.NewRecorder()
	h.GroupDelete(w, r)
	if w.Code != http.StatusConflict || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("occupied group reported success: %d %v", w.Code, w.Header())
	}
}

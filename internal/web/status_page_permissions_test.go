package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestStatusPageFormAndSavePreserveHiddenMembership(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	m := &storage.Monitor{Name: "private-service", Type: "http", Target: "https://private.example.test", Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	sp := &storage.StatusPage{Title: "Services", Slug: "services", PasswordHash: "saved-password", AnalyticsScript: "legacy script"}
	if err := store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	key := &config.APIKeyConfig{Name: "editor", Permissions: []string{"status_pages.read", "status_pages.update"}}
	request := func(method, path string, form url.Values) *http.Request {
		r := adminRequest(method, path, form)
		r.SetPathValue("id", strconvI(sp.ID))
		return r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
	}
	w := httptest.NewRecorder()
	h.StatusPageForm(w, request("GET", "/status-pages/"+strconvI(sp.ID)+"/edit", nil))
	for _, secret := range []string{m.Name, m.Target, "monitor_" + strconvI(m.ID) + "_enabled", `name="monitors_present"`, `name="analytics_script"`, sp.PasswordHash, sp.AnalyticsScript} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("form disclosed hidden value/control %q", secret)
		}
	}
	form := url.Values{"title": {"Renamed"}, "slug": {sp.Slug}}
	w = httptest.NewRecorder()
	h.StatusPageUpdate(w, request("POST", "/status-pages/"+strconvI(sp.ID), form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("metadata edit failed: %d %s", w.Code, w.Body.String())
	}
	stored, err := store.GetStatusPage(ctx, sp.ID)
	members, memberErr := store.ListStatusPageMonitors(ctx, sp.ID)
	if err != nil || memberErr != nil || stored.PasswordHash != sp.PasswordHash || stored.AnalyticsScript != sp.AnalyticsScript || len(members) != 1 {
		t.Fatalf("hidden values changed: %+v %v %v %v", stored, members, err, memberErr)
	}
	form.Set("monitors_present", "1")
	w = httptest.NewRecorder()
	h.StatusPageUpdate(w, request("POST", "/status-pages/"+strconvI(sp.ID), form))
	if w.Code != http.StatusForbidden || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unauthorized membership clear reported success: %d", w.Code)
	}
}

func TestStatusPageFormRejectsMalformedMembership(t *testing.T) {
	for _, form := range []url.Values{
		{"monitor_bad_enabled": {"on"}},
		{"monitor_0_enabled": {"on"}},
		{"monitor_1_enabled": {"on", "on"}},
		{"monitor_1_enabled": {"on"}, "monitor_1_sort": {"bad"}},
		{"monitor_1_enabled": {"on"}, "monitor_1_sort": {"-1"}},
	} {
		r := adminRequest("POST", "/status-pages", form)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if _, err := parseStatusPageForm(r); err == nil {
			t.Fatalf("invalid membership silently dropped: %v", form)
		}
	}
}

func TestStatusPageWriteOnlyWebSaveReturnsToReadableDestination(t *testing.T) {
	h, _ := storeHandler(t)
	h.cfg.Server.BasePath = "/asura"
	key := &config.APIKeyConfig{Name: "writer", Permissions: []string{"status_pages.create", "settings.read"}}
	r := adminRequest("POST", "/asura/status-pages", url.Values{"title": {"Page"}, "slug": {"page"}})
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
	w := httptest.NewRecorder()
	h.StatusPageCreate(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/asura/settings" {
		t.Fatalf("write-only redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
}

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
)

func webLoginCookie(t *testing.T, srv *Server, key string) *http.Cookie {
	t.Helper()
	form := url.Values{"api_key": {key}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

type cancelOnFlush struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *cancelOnFlush) Flush() {
	w.ResponseRecorder.Flush()
	w.cancel()
}

func TestWebReadPermissions(t *testing.T) {
	for _, tc := range []struct {
		path string
		perm string
	}{
		{"/", "monitors.read"},
		{"/monitors", "monitors.read"},
		{"/monitors/new", "monitors.write"},
		{"/monitors/999", "monitors.read"},
		{"/monitors/999/edit", "monitors.write"},
		{"/monitors/999/chart", "monitors.read"},
		{"/incidents", "incidents.read"},
		{"/incidents/999", "incidents.read"},
		{"/groups", "monitors.read"},
		{"/groups/999", "monitors.read"},
		{"/tags", "monitors.read"},
		{"/notifications", "notifications.read"},
		{"/notifications/history", "notifications.read"},
		{"/escalation-policies", "escalation_policies.read"},
		{"/sla", "monitors.read"},
		{"/sla/export", "monitors.read"},
		{"/maintenance", "maintenance.read"},
		{"/logs", "metrics.read"},
		{"/audit", "metrics.read"},
		{"/proxies", "monitors.read"},
		{"/proxies/new", "monitors.write"},
		{"/proxies/999/edit", "monitors.write"},
		{"/on-call", "notifications.read"},
		{"/agents", "monitors.read"},
		{"/status-pages", "monitors.read"},
		{"/status-pages/new", "monitors.write"},
		{"/status-pages/999/edit", "monitors.write"},
		{"/settings", "metrics.read"},
		{"/events", "monitors.read"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			srv, _ := testServer(t)
			t.Cleanup(srv.Close)
			key := config.APIKeyConfig{Name: "scoped", Hash: config.HashAPIKey("scoped-key")}
			for _, perm := range config.AllPermissions {
				if perm != tc.perm {
					key.Permissions = append(key.Permissions, perm)
				}
			}
			srv.cfg.Auth.APIKeys = append(srv.cfg.Auth.APIKeys, key)
			cookie := webLoginCookie(t, srv, "scoped-key")
			for _, allowed := range []bool{false, true} {
				if allowed {
					srv.cfg.Auth.APIKeys[len(srv.cfg.Auth.APIKeys)-1].Permissions = []string{tc.perm}
				}
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				req.AddCookie(cookie)
				ctx, cancel := context.WithCancel(req.Context())
				w := httptest.NewRecorder()
				srv.ServeHTTP(&cancelOnFlush{w, cancel}, req.WithContext(ctx))
				cancel()
				if !allowed {
					want := http.StatusForbidden
					if tc.path == "/" {
						want = http.StatusSeeOther
						if got := w.Header().Get("Location"); got != "/incidents" {
							t.Errorf("home without monitor access redirects to %q, want /incidents", got)
						}
					}
					if w.Code != want {
						t.Errorf("without %s: status = %d, want %d", tc.perm, w.Code, want)
					}
				}
				if allowed && w.Code != http.StatusOK && w.Code != http.StatusNotFound && (w.Code != http.StatusSeeOther || w.Header().Get("Location") == "/login") {
					t.Errorf("with %s: unexpected status %d, location %q", tc.perm, w.Code, w.Header().Get("Location"))
				}
			}
		})
	}
}

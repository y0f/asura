package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
)

type settingsNoReads struct{ storage.Store }

func TestSettingsSkipsUnavailableInventory(t *testing.T) {
	h := testWebHandler(t)
	// Calling any store method would panic. A settings-only reader has no
	// authority to query either database size or the monitor inventory.
	h.store = settingsNoReads{}
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{Permissions: []string{"settings.read"}}))
	w := httptest.NewRecorder()
	h.Settings(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Database size") || strings.Contains(w.Body.String(), "Visible monitors") {
		t.Fatalf("settings-only reader saw an unauthorized inventory fact: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.Settings(w, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing identity accessed settings: %d", w.Code)
	}
}

func TestSettingsMonitorCountUsesCurrentReadScope(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	visible := &storage.Monitor{Name: "visible", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10}
	private := &storage.Monitor{Name: "private", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10}
	for _, monitor := range []*storage.Monitor{visible, private} {
		if err := store.CreateMonitor(ctx, monitor); err != nil {
			t.Fatal(err)
		}
	}
	h.cfg.Auth.Roles = []config.RoleConfig{{Name: "scoped-reader", Permissions: []string{"monitors.read"}}}
	h.cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "reader", Hash: config.HashAPIKey("synthetic-key"), Permissions: []string{"settings.read"}, RoleBindings: []config.RoleBindingConfig{{Role: "scoped-reader", Scope: config.MonitorAccess{MonitorIDs: []int64{visible.ID}}}}}}
	if err := h.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)
	r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &h.cfg.Auth.APIKeys[0]))
	w := httptest.NewRecorder()
	h.Settings(w, r)
	count := regexp.MustCompile(`Visible monitors</dt>\s*<dd[^>]*>\s*1\s*</dd>`)
	if w.Code != http.StatusOK || !count.Match(w.Body.Bytes()) || strings.Contains(w.Body.String(), "Database size") {
		t.Fatalf("settings count escaped scope or disclosed database size: %d, fact=%q", w.Code, regexp.MustCompile(`Visible monitors.{0,160}`).FindString(w.Body.String()))
	}
}

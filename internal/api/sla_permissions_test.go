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

	"github.com/y0f/asura/v2/internal/api"
	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/sla"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestSLARequestsScopeRowsAndMonitorNamesIndependently(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	monitors := make([]*storage.Monitor, 2)
	for i := range monitors {
		monitors[i] = &storage.Monitor{Name: fmt.Sprintf("private-sla-name-%d", i), Type: "http", Interval: 60, Timeout: 10, SLATarget: 99.9}
		if err := env.store.CreateMonitor(ctx, monitors[i]); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Auth.Roles = []config.RoleConfig{{Name: "sla", Permissions: []string{"sla.read"}}, {Name: "monitor", Permissions: []string{"monitors.read"}}}
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "reader", Hash: config.HashAPIKey("reader"), RoleBindings: []config.RoleBindingConfig{
		{Role: "sla", Scope: config.MonitorAccess{MonitorIDs: []int64{monitors[0].ID}}},
		{Role: "monitor", Scope: config.MonitorAccess{MonitorIDs: []int64{monitors[1].ID}}},
	}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	h := api.New(cfg, env.store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	do := func(handler http.HandlerFunc, id int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/reports/sla", nil)
		r.SetPathValue("id", fmt.Sprint(id))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, &cfg.Auth.APIKeys[0]))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	for _, handler := range []http.HandlerFunc{h.SLAReport, h.SLAReportExport} {
		w := do(handler, 0)
		var response struct {
			Entries []*sla.ReportEntry `json:"entries"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || len(response.Entries) != 1 || response.Entries[0].MonitorID != monitors[0].ID || response.Entries[0].MonitorName != "" {
			t.Fatalf("SLA report crossed action/resource scopes: %d %s, %v", w.Code, w.Body.String(), err)
		}
		if strings.Contains(w.Body.String(), "private-sla-name") || strings.Contains(w.Body.String(), "MonitorGroupID") {
			t.Fatalf("report leaked internal names/metadata: %s", w.Body.String())
		}
	}
	w := do(h.MonitorSLA, monitors[0].ID)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), monitors[0].Name) {
		t.Fatalf("SLA-only monitor response: %d %s", w.Code, w.Body.String())
	}
	w = do(h.MonitorSLA, monitors[1].ID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("monitor read widened SLA access: %d %s", w.Code, w.Body.String())
	}
}

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestDiagnosticWebReadSurfacesRequireExplicitPermission(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	mon := &storage.Monitor{Name: "monitor", Type: "http", Target: "https://example.test", Enabled: true, Interval: 60, Timeout: 10,
		Assertions: json.RawMessage(`{"groups":[{"conditions":[{"type":"body_contains","value":"private-assertion-marker"}]}]}`)}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertCheckResult(ctx, &storage.CheckResult{MonitorID: mon.ID, Status: "down", Message: "private-check-marker"}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertContentChange(ctx, &storage.ContentChange{MonitorID: mon.ID, Diff: "private-diff-marker", OldHash: "private-old-marker", NewHash: "private-new-marker"}); err != nil {
		t.Fatal(err)
	}
	inc, _, err := store.RecordIncidentFailure(ctx, mon.ID, "critical", "private-cause-marker")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		id      int64
		handler http.HandlerFunc
	}{
		{"dashboard", 0, h.Dashboard}, {"monitor", mon.ID, h.MonitorDetail}, {"editor", mon.ID, h.MonitorForm}, {"incidents", 0, h.Incidents}, {"incident", inc.ID, h.IncidentDetail},
	} {
		for _, diagnostics := range []bool{false, true} {
			permissions := []string{"monitors.read", "monitors.update", "incidents.read"}
			if diagnostics {
				permissions = append(permissions, "diagnostics.read")
			}
			r := adminRequest("GET", "/", nil)
			if tc.id > 0 {
				r.SetPathValue("id", strconvI(tc.id))
			}
			r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{Permissions: permissions}))
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-") != diagnostics {
				t.Fatalf("%s diagnostics=%t boundary: status=%d contains=%t", tc.name, diagnostics, w.Code, strings.Contains(w.Body.String(), "private-"))
			}
		}
	}
	stored, err := store.GetMonitor(ctx, mon.ID)
	if err != nil || string(stored.Assertions) != string(mon.Assertions) {
		t.Fatal("read projection modified stored assertions")
	}
}

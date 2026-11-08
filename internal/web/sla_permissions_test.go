package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/sla"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestSLAWebHidesUnrelatedRowsNamesAndLinks(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	monitors := []*storage.Monitor{
		{Name: "private SLA monitor", Type: "http", Interval: 60, Timeout: 10, SLATarget: 99.9},
		{Name: "excluded SLA monitor", Type: "http", Interval: 60, Timeout: 10, SLATarget: 99},
	}
	for _, monitor := range monitors {
		if err := store.CreateMonitor(ctx, monitor); err != nil {
			t.Fatal(err)
		}
	}
	for _, monitorRead := range []bool{false, true} {
		permissions := []string{"sla.read"}
		if monitorRead {
			permissions = append(permissions, "monitors.read")
		}
		cfg := config.Defaults()
		cfg.Auth.Roles = []config.RoleConfig{{Name: "scoped", Permissions: permissions}}
		cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "reader", Hash: config.HashAPIKey("reader"), RoleBindings: []config.RoleBindingConfig{{Role: "scoped", Scope: config.MonitorAccess{MonitorIDs: []int64{monitors[0].ID}}}}}}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, export := range []bool{false, true} {
			r := httptest.NewRequest("GET", "/sla", nil).WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, &cfg.Auth.APIKeys[0]))
			w := httptest.NewRecorder()
			if export {
				h.SLAExport(w, r)
			} else {
				h.SLAReport(w, r)
			}
			body := w.Body.String()
			if w.Code != http.StatusOK || strings.Contains(body, monitors[1].Name) || strings.Contains(body, monitors[0].Name) != monitorRead {
				t.Fatalf("SLA read=%v export=%v: %d %s", monitorRead, export, w.Code, body)
			}
			if export {
				var response struct {
					Entries []*sla.ReportEntry `json:"entries"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Entries) != 1 || response.Entries[0].MonitorID != monitors[0].ID {
					t.Fatalf("export rows: %+v, %v", response, err)
				}
			} else if strings.Contains(body, `href="/monitors/`+strconvI(monitors[0].ID)+`"`) != monitorRead {
				t.Fatalf("monitor link did not follow independent scope: %s", body)
			}
		}
	}
}

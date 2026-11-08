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

func TestDiagnosticAPIReadsRequireExplicitPermission(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		for _, name := range []string{"reader", "debugger"} {
			permissions := []string{"monitors.read", "incidents.read", "monitors.update"}
			if name == "debugger" {
				permissions = append(permissions, "diagnostics.read")
			}
			c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: name, Hash: config.HashAPIKey(name), Permissions: permissions})
		}
	}, false, nil)
	ctx := context.Background()
	mon := &storage.Monitor{Name: "monitor", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1,
		Assertions: json.RawMessage(`{"groups":[{"conditions":[{"type":"body_contains","value":"private-assertion"}]}]}`)}
	if err := env.store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	if err := env.store.InsertCheckResult(ctx, &storage.CheckResult{MonitorID: mon.ID, Status: "down", Message: "private-error", Headers: "private-cookie", Body: "private-body", BodyHash: "private-hash"}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.InsertContentChange(ctx, &storage.ContentChange{MonitorID: mon.ID, Diff: "private-diff", OldHash: "private-old", NewHash: "private-new"}); err != nil {
		t.Fatal(err)
	}
	inc, _, err := env.store.RecordIncidentFailure(ctx, mon.ID, "critical", "private-cause")
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/monitors/%d", mon.ID)
	for _, path := range []string{base, "/api/v1/monitors", base + "/checks", base + "/changes", "/api/v1/incidents", fmt.Sprintf("/api/v1/incidents/%d", inc.ID)} {
		for _, key := range []string{"reader", "debugger"} {
			w := env.do("GET", path, "", map[string]string{"X-API-Key": key})
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-") != (key == "debugger") {
				t.Fatalf("%s %s diagnostic boundary: %d %s", key, path, w.Code, w.Body)
			}
		}
	}
	reader := map[string]string{"X-API-Key": "reader"}
	w := env.do("GET", base, "", reader)
	var edit map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &edit); err != nil {
		t.Fatal(err)
	}
	edit["name"] = "renamed"
	body, _ := json.Marshal(edit)
	w = env.do("PUT", base, string(body), reader)
	if w.Code != 200 {
		t.Fatalf("unrelated edit: %d %s", w.Code, w.Body)
	}
	stored, err := env.store.GetMonitor(ctx, mon.ID)
	if err != nil || string(stored.Assertions) != string(mon.Assertions) {
		t.Fatal("hidden assertions lost on unrelated edit")
	}
	edit["clear_secrets"] = []string{"assertions"}
	body, _ = json.Marshal(edit)
	w = env.do("PUT", base, string(body), reader)
	stored, err = env.store.GetMonitor(ctx, mon.ID)
	if w.Code != 200 || err != nil || string(stored.Assertions) != `[]` {
		t.Fatalf("explicit assertion removal failed: %d %v", w.Code, err)
	}
}

func TestDiagnosticReadsRecheckGrantAfterGroupMove(t *testing.T) {
	for _, endpoint := range []string{"checks", "changes", "timeline"} {
		t.Run(endpoint, func(t *testing.T) {
			env := newTestEnv(t, nil, false, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			group := &storage.MonitorGroup{Name: "debuggable"}
			if err := env.store.CreateMonitorGroup(ctx, group); err != nil {
				t.Fatal(err)
			}
			mon := &storage.Monitor{Name: "monitor", Type: "http", GroupID: &group.ID, Interval: 60, Timeout: 10}
			if err := env.store.CreateMonitor(ctx, mon); err != nil {
				t.Fatal(err)
			}
			inc, _, err := env.store.RecordIncidentFailure(ctx, mon.ID, "critical", "private-cause")
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Auth.Roles = []config.RoleConfig{{Name: "debugger", Permissions: []string{"diagnostics.read"}}}
			cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "reader", Hash: config.HashAPIKey("reader"), Permissions: []string{"monitors.read", "incidents.read"}, RoleBindings: []config.RoleBindingConfig{{Role: "debugger", Scope: config.MonitorAccess{GroupIDs: []int64{group.ID}}}}}}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			read, resume := make(chan struct{}), make(chan struct{})
			var store storage.Store = heldMonitorReadStore{Store: env.store, read: read, resume: resume}
			id := mon.ID
			if endpoint == "timeline" {
				store = heldIncidentLookupStore{Store: env.store, read: read, resume: resume}
				id = inc.ID
			}
			h := api.New(cfg, store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			handler := h.ListChecks
			if endpoint == "changes" {
				handler = h.ListChanges
			}
			if endpoint == "timeline" {
				handler = h.GetIncident
			}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.SetPathValue("id", fmt.Sprint(id))
			r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, &cfg.Auth.APIKeys[0]))
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); handler(w, r) }()
			select {
			case <-read:
			case <-ctx.Done():
				t.Fatal("lookup not reached")
			}
			_, moveErr := env.store.BulkSetMonitorGroup(ctx, []int64{mon.ID}, nil)
			close(resume)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("read did not finish")
			}
			if moveErr != nil {
				t.Fatal(moveErr)
			}
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("revoked diagnostics survived current ordinary read: %d %s", w.Code, w.Body)
			}
		})
	}
}

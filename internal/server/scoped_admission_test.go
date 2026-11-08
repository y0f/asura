package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func scopedServer(t *testing.T, base string, headlessMode bool, permissions []string) (*Server, storage.Store, []*storage.Monitor, []*storage.Incident) {
	t.Helper()
	srv, store := regressionServer(t, func(cfg *config.Config) {
		cfg.Server.BasePath = base
		if headlessMode {
			headless(cfg)
		}
		cfg.Auth.Roles = []config.RoleConfig{{Name: "scope", Permissions: permissions}}
		cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "scoped", Hash: config.HashAPIKey("scoped-key"), RoleBindings: []config.RoleBindingConfig{{Role: "scope", Scope: config.MonitorAccess{GroupIDs: []int64{1}}}}}}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(srv.Close)
	ctx := context.Background()
	monitors := make([]*storage.Monitor, 2)
	incidents := make([]*storage.Incident, 2)
	for i := range monitors {
		group := &storage.MonitorGroup{Name: fmt.Sprintf("group%d", i)}
		if err := store.CreateMonitorGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
		if group.ID != int64(i+1) {
			t.Fatal("unexpected fixture group ID")
		}
		monitors[i] = &storage.Monitor{Name: fmt.Sprintf("monitor-secret-name-%d", i), Type: "http", Target: "https://example.test", GroupID: &group.ID, Interval: 60, Timeout: 10, Enabled: true, SLATarget: 99.9}
		if err := store.CreateMonitor(ctx, monitors[i]); err != nil {
			t.Fatal(err)
		}
		var err error
		incidents[i], _, err = store.RecordIncidentFailure(ctx, monitors[i].ID, "warning", fmt.Sprintf("incident-private-cause-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	return srv, store, monitors, incidents
}

func TestScopedRouteAdmissionKeepsEveryReadObjectBound(t *testing.T) {
	permissions := []string{"monitors.read", "incidents.read", "sla.read", "maintenance.read", "notification_history.read", "request_logs.read"}
	for _, base := range []string{"", "/console"} {
		for _, headlessMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/headless=%v", base, headlessMode), func(t *testing.T) {
				srv, _, monitors, incidents := scopedServer(t, base, headlessMode, permissions)
				var cookie *http.Cookie
				if !headlessMode {
					cookie = webLoginCookie(t, srv, "scoped-key")
				}
				do := func(path string, web bool) *httptest.ResponseRecorder {
					r := httptest.NewRequest("GET", base+path, nil)
					if web {
						r.AddCookie(cookie)
						r.Header.Set("Origin", "http://example.com")
					} else {
						r.Header.Set("X-API-Key", "scoped-key")
					}
					w := httptest.NewRecorder()
					srv.ServeHTTP(w, r)
					return w
				}
				for _, tc := range []struct {
					path     string
					web      bool
					incident bool
				}{
					{"/api/v1/monitors/%d", false, false}, {"/api/v1/monitors/%d/checks", false, false}, {"/api/v1/monitors/%d/changes", false, false}, {"/api/v1/monitors/%d/metrics", false, false}, {"/api/v1/monitors/%d/chart", false, false}, {"/api/v1/monitors/%d/sla", false, false}, {"/api/v1/incidents/%d", false, true},
					{"/monitors/%d", true, false}, {"/monitors/%d/chart", true, false}, {"/incidents/%d", true, true},
				} {
					if tc.web && headlessMode {
						continue
					}
					for i := range monitors {
						id := monitors[i].ID
						if tc.incident {
							id = incidents[i].ID
						}
						w := do(fmt.Sprintf(tc.path, id), tc.web)
						want := 200
						if i == 1 {
							want = 404
						}
						if w.Code != want || strings.Contains(w.Body.String(), monitors[1].Name) || strings.Contains(w.Body.String(), incidents[1].Cause) {
							t.Fatalf("%s object%d: %d want%d: %.300s", tc.path, id, w.Code, want, w.Body.String())
						}
					}
				}
				for _, path := range []string{"/api/v1/monitors?per_page=1", "/api/v1/incidents?per_page=1", "/api/v1/overview", "/api/v1/reports/sla", "/api/v1/reports/sla/export", "/api/v1/maintenance", "/api/v1/notifications/history", "/api/v1/request-logs", "/api/v1/request-logs/stats"} {
					w := do(path, false)
					if w.Code != 200 || strings.Contains(w.Body.String(), monitors[1].Name) || strings.Contains(w.Body.String(), incidents[1].Cause) {
						t.Fatalf("collection %s: %d %.300s", path, w.Code, w.Body.String())
					}
				}
				if !headlessMode {
					for _, path := range []string{"/", "/monitors", "/incidents", "/sla", "/sla/export", "/maintenance", "/notifications/history", "/logs"} {
						w := do(path, true)
						if w.Code != 200 || strings.Contains(w.Body.String(), monitors[1].Name) || strings.Contains(w.Body.String(), incidents[1].Cause) {
							t.Fatalf("web collection %s: %d %.300s", path, w.Code, w.Body.String())
						}
					}
				}
			})
		}
	}
}

func TestScopedSessionUsesCurrentBindingsAndLanding(t *testing.T) {
	for _, base := range []string{"", "/console"} {
		t.Run(base, func(t *testing.T) {
			srv, _, monitors, _ := scopedServer(t, base, false, []string{"monitors.read"})
			cookie := webLoginCookie(t, srv, "scoped-key")
			do := func(path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", base+path, nil)
				r.AddCookie(cookie)
				r.Header.Set("Origin", "http://example.com")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				return w
			}
			// Keep this session and key identity, but replace its granted role/scope.
			srv.cfg.Auth.Roles[0].Permissions = []string{"incidents.read"}
			srv.cfg.Auth.APIKeys[0].RoleBindings[0].Scope = config.MonitorAccess{GroupIDs: []int64{2}}
			if err := srv.cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if w := do("/monitors/" + fmt.Sprint(monitors[0].ID)); w.Code != 403 {
				t.Fatalf("removed capability survived session: %d", w.Code)
			}
			if w := do("/"); w.Code != 303 || w.Header().Get("Location") != base+"/incidents" {
				t.Fatalf("current role landing: %d %v", w.Code, w.Header())
			}
			if w := do("/incidents"); w.Code != 200 || strings.Contains(w.Body.String(), "incident-private-cause-") {
				t.Fatalf("ordinary incident read exposed diagnostics: %d", w.Code)
			}
			srv.cfg.Auth.Roles[0].Permissions = []string{"incidents.read", "diagnostics.read"}
			if err := srv.cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if w := do("/incidents"); w.Code != 200 || !strings.Contains(w.Body.String(), "incident-private-cause-1") || strings.Contains(w.Body.String(), "incident-private-cause-0") {
				t.Fatalf("new session scope not applied: %d %.300s", w.Code, w.Body.String())
			}
			srv.cfg.Auth.Roles[0].Permissions = []string{"monitors.read"}
			if err := srv.cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if w := do("/monitors/" + fmt.Sprint(monitors[0].ID)); w.Code != 404 {
				t.Fatalf("old object binding survived session: %d", w.Code)
			}
			if w := do("/monitors/" + fmt.Sprint(monitors[1].ID)); w.Code != 200 {
				t.Fatalf("new object binding unavailable: %d", w.Code)
			}
			srv.cfg.Auth.APIKeys[0].Hash = config.HashAPIKey("rotated")
			if err := srv.cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if w := do("/monitors"); w.Code != 303 || w.Header().Get("Location") != base+"/login" {
				t.Fatalf("key rotation retained session: %d %v", w.Code, w.Header())
			}
		})
	}
}

func TestScopedBulkChecksAllIDsBeforeEffects(t *testing.T) {
	for _, base := range []string{"", "/console"} {
		for _, web := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/web=%v", base, web), func(t *testing.T) {
				srv, store, monitors, _ := scopedServer(t, base, false, []string{"monitors.read", "monitors.pause", "monitors.resume"})
				var cookie *http.Cookie
				if web {
					cookie = webLoginCookie(t, srv, "scoped-key")
				}
				do := func(ids []int64) *httptest.ResponseRecorder {
					var r *http.Request
					if web {
						form := url.Values{"action": {"pause"}}
						for _, id := range ids {
							form.Add("ids[]", fmt.Sprint(id))
						}
						r = httptest.NewRequest("POST", base+"/monitors/bulk", strings.NewReader(form.Encode()))
						r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						r.AddCookie(cookie)
						r.Header.Set("Origin", "http://example.com")
					} else {
						body, _ := json.Marshal(map[string]any{"action": "pause", "ids": ids})
						r = httptest.NewRequest("POST", base+"/api/v1/monitors/bulk", strings.NewReader(string(body)))
						r.Header.Set("X-API-Key", "scoped-key")
					}
					w := httptest.NewRecorder()
					srv.ServeHTTP(w, r)
					return w
				}
				if w := do([]int64{monitors[0].ID, monitors[1].ID}); w.Code != 404 {
					t.Fatalf("mixed bulk admitted: %d %.300s", w.Code, w.Body.String())
				}
				for _, m := range monitors {
					stored, err := store.GetMonitor(context.Background(), m.ID)
					if err != nil || !stored.Enabled {
						t.Fatalf("denied bulk partially wrote: %+v %v", stored, err)
					}
				}
				want := 200
				if web {
					want = 303
				}
				if w := do([]int64{monitors[0].ID}); w.Code != want {
					t.Fatalf("authorized scoped bulk: %d %.300s", w.Code, w.Body.String())
				}
				stored, err := store.GetMonitor(context.Background(), monitors[0].ID)
				if err != nil || stored.Enabled {
					t.Fatalf("authorized pause missing: %+v %v", stored, err)
				}
			})
		}
	}
}

func TestScopedCapabilitiesNeverUnionDifferentActionTargets(t *testing.T) {
	srv, store, monitors, _ := scopedServer(t, "", false, []string{"monitors.read"})
	srv.cfg.Auth.Roles = append(srv.cfg.Auth.Roles, config.RoleConfig{Name: "editor", Permissions: []string{"monitors.update", "monitors.clone"}})
	srv.cfg.Auth.APIKeys[0].RoleBindings = append(srv.cfg.Auth.APIKeys[0].RoleBindings, config.RoleBindingConfig{Role: "editor", Scope: config.MonitorAccess{GroupIDs: []int64{2}}})
	if err := srv.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cookie := webLoginCookie(t, srv, "scoped-key")
	for _, m := range monitors {
		for _, tc := range []struct {
			method, path string
			web          bool
		}{{"GET", "/monitors/%d/edit", true}, {"POST", "/monitors/%d/clone", true}, {"POST", "/api/v1/monitors/%d/clone", false}} {
			r := httptest.NewRequest(tc.method, fmt.Sprintf(tc.path, m.ID), nil)
			if tc.web {
				r.AddCookie(cookie)
				r.Header.Set("Origin", "http://example.com")
			} else {
				r.Header.Set("X-API-Key", "scoped-key")
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatalf("cross-action capability union authorized %s%d: %d %.300s", tc.path, m.ID, w.Code, w.Body.String())
			}
		}
	}
	result, err := store.ListMonitors(context.Background(), storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil || result.Total != 2 {
		t.Fatalf("denied clone wrote a row: %+v %v", result, err)
	}
}

func TestMetricsReadIsGlobalOperationalExportWithoutMonitorAPIRead(t *testing.T) {
	srv, store, monitors, _ := scopedServer(t, "", true, []string{"monitors.read"})
	srv.cfg.Auth.Roles = nil
	srv.cfg.Auth.APIKeys[0].RoleBindings = nil
	srv.cfg.Auth.APIKeys[0].Permissions = []string{"metrics.read"}
	if err := srv.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	monitors[0].Target = "https://private-target.example.test"
	if err := store.UpdateMonitor(context.Background(), monitors[0]); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/metrics", 200}, {"/api/v1/monitors", 403}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("X-API-Key", "scoped-key")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("metrics grant %s: %d %.300s", tc.path, w.Code, w.Body.String())
		}
		if tc.path == "/metrics" && (!strings.Contains(w.Body.String(), monitors[0].Name) || !strings.Contains(w.Body.String(), monitors[1].Name) || strings.Contains(w.Body.String(), "private-target")) {
			t.Fatalf("global operational export contract: %.300s", w.Body.String())
		}
	}
	srv.cfg.Auth.Roles = []config.RoleConfig{{Name: "invalid-scoped-export", Permissions: []string{"metrics.read"}}}
	srv.cfg.Auth.APIKeys[0].Permissions = nil
	srv.cfg.Auth.APIKeys[0].RoleBindings = []config.RoleBindingConfig{{Role: "invalid-scoped-export", Scope: config.MonitorAccess{MonitorIDs: []int64{monitors[0].ID}}}}
	if err := srv.cfg.Validate(); err == nil {
		t.Fatal("global metrics authority accepted an object-scoped binding")
	}
}

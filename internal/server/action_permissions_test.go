package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

type permissionRoute struct {
	method, path string
	scopes       []string
	web          bool
}

// This table specifies the public authorization contract independently of the
// router. Tests remove each required grant while retaining all other grants.
func permissionRoutes() []permissionRoute {
	var routes []permissionRoute
	add := func(web bool, method, path string, scopes ...string) {
		routes = append(routes, permissionRoute{method, path, scopes, web})
	}
	for _, resource := range []struct{ scope, path string }{
		{"monitors", "monitors"}, {"incidents", "incidents"}, {"notifications", "notifications"},
		{"notification_history", "notifications/history"}, {"escalation_policies", "escalation-policies"},
		{"maintenance", "maintenance"}, {"groups", "groups"}, {"tags", "tags"}, {"proxies", "proxies"},
		{"agents", "agents"}, {"status_pages", "status-pages"}, {"on_call", "on-call"}, {"audit", "audit"},
	} {
		add(false, "GET", "/api/v1/"+resource.path, resource.scope+".read")
		add(true, "GET", "/"+resource.path, resource.scope+".read")
	}
	for _, resource := range []struct{ scope, path string }{
		{"monitors", "monitors"}, {"notifications", "notifications"}, {"escalation_policies", "escalation-policies"},
		{"maintenance", "maintenance"}, {"groups", "groups"}, {"tags", "tags"}, {"proxies", "proxies"},
		{"status_pages", "status-pages"}, {"on_call", "on-call"}, {"agents", "agents"},
	} {
		add(false, "POST", "/api/v1/"+resource.path, resource.scope+".create")
		add(true, "POST", "/"+resource.path, resource.scope+".create")
		add(false, "DELETE", "/api/v1/"+resource.path+"/999", resource.scope+".delete")
		add(true, "POST", "/"+resource.path+"/999/delete", resource.scope+".delete")
		if resource.scope != "agents" {
			add(false, "PUT", "/api/v1/"+resource.path+"/999", resource.scope+".update")
			if resource.scope != "on_call" {
				add(true, "POST", "/"+resource.path+"/999", resource.scope+".update")
			}
		}
	}
	for _, resource := range []struct{ scope, path string }{
		{"monitors", "monitors"}, {"proxies", "proxies"}, {"status_pages", "status-pages"},
	} {
		add(true, "GET", "/"+resource.path+"/new", resource.scope+".create")
		add(true, "GET", "/"+resource.path+"/999/edit", resource.scope+".read", resource.scope+".update")
	}
	for _, action := range []string{"pause", "resume", "status"} {
		add(false, "POST", "/api/v1/monitors/999/"+action, "monitors."+action)
		add(true, "POST", "/monitors/999/"+action, "monitors."+action)
	}
	add(false, "POST", "/api/v1/monitors/999/clone", "monitors.read", "monitors.clone")
	add(true, "POST", "/monitors/999/clone", "monitors.read", "monitors.clone")
	for _, action := range []struct{ path, scope string }{{"ack", "acknowledge"}, {"resolve", "resolve"}} {
		add(false, "POST", "/api/v1/incidents/999/"+action.path, "incidents."+action.scope)
		add(true, "POST", "/incidents/999/"+action.path, "incidents."+action.scope)
	}
	add(false, "POST", "/api/v1/incidents/999/severity", "incidents.severity")
	add(false, "DELETE", "/api/v1/incidents/999", "incidents.delete")
	add(true, "POST", "/incidents/999/delete", "incidents.delete")
	for _, action := range []struct{ path, scope string }{
		{"notifications/999/test", "notifications.test"}, {"maintenance/999/toggle", "maintenance.toggle"},
	} {
		add(false, "POST", "/api/v1/"+action.path, action.scope)
		add(true, "POST", "/"+action.path, action.scope)
	}
	add(true, "POST", "/on-call/999/override", "on_call.override")
	for _, resource := range []struct{ scope, path string }{
		{"monitors", "monitors/999"}, {"monitors", "monitors/999/checks"}, {"monitors", "monitors/999/metrics"},
		{"monitors", "monitors/999/changes"}, {"monitors", "monitors/999/chart"}, {"monitors", "overview"},
		{"incidents", "incidents/999"}, {"escalation_policies", "escalation-policies/999"}, {"proxies", "proxies/999"},
		{"status_pages", "status-pages/999"}, {"agents", "agents/status"}, {"on_call", "on-call/current"},
		{"sla", "monitors/999/sla"}, {"sla", "reports/sla"}, {"sla", "reports/sla/export"},
		{"request_logs", "request-logs"}, {"request_logs", "request-logs/stats"}, {"database", "db/size"},
		{"subscribers", "status-pages/999/subscribers"}, {"monitors", "events"},
	} {
		add(false, "GET", "/api/v1/"+resource.path, resource.scope+".read")
	}
	for _, resource := range []struct{ scope, path string }{
		{"monitors", "/monitors/999"}, {"monitors", "/monitors/999/chart"}, {"incidents", "/incidents/999"},
		{"groups", "/groups/999"}, {"sla", "/sla"}, {"sla", "/sla/export"}, {"request_logs", "/logs"},
		{"settings", "/settings"}, {"monitors", "/events"},
	} {
		add(true, "GET", resource.path, resource.scope+".read")
	}
	add(false, "GET", "/metrics", "metrics.read")
	add(false, "DELETE", "/api/v1/status-pages/999/subscribers/999", "subscribers.delete")
	add(false, "POST", "/api/v1/db/vacuum", "database.maintain")
	add(true, "POST", "/settings/vacuum", "database.maintain")
	return routes
}

func TestRoutePermissionBoundaries(t *testing.T) {
	for _, basePath := range []string{"", "/asura"} {
		for _, headless := range []bool{false, true} {
			t.Run(fmt.Sprintf("base=%s/headless=%t", basePath, headless), func(t *testing.T) {
				srv, _ := regressionServer(t, func(cfg *config.Config) {
					cfg.Server.BasePath = basePath
					cfg.Server.RateLimitBurst = 10000
					cfg.Server.RateLimitPerSec = 10000
					enabled := !headless
					cfg.Server.WebUIEnabled = &enabled
				})
				t.Cleanup(srv.Close)
				var cookie *http.Cookie
				if !headless {
					req := httptest.NewRequest("POST", basePath+"/login", strings.NewReader(url.Values{"api_key": {regressionAdminKey}}.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					w := httptest.NewRecorder()
					srv.ServeHTTP(w, req)
					if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
						t.Fatalf("login: %d %s", w.Code, w.Body.String())
					}
					cookie = w.Result().Cookies()[0]
				}
				key := &srv.cfg.Auth.APIKeys[0]
				key.SuperAdmin = false
				for _, route := range permissionRoutes() {
					if headless && route.web {
						continue
					}
					t.Run(route.method+" "+route.path, func(t *testing.T) {
						request := func() int {
							body, contentType := "{}", "application/json"
							if route.web {
								body, contentType = "", "application/x-www-form-urlencoded"
							}
							req := httptest.NewRequest(route.method, basePath+route.path, strings.NewReader(body))
							req.Header.Set("Content-Type", contentType)
							if route.web {
								req.AddCookie(cookie)
								req.Header.Set("Origin", "http://example.com")
							} else {
								req.Header.Set("X-API-Key", regressionAdminKey)
							}
							ctx, cancel := context.WithCancel(req.Context())
							defer cancel()
							w := httptest.NewRecorder()
							srv.ServeHTTP(&cancelOnFlush{w, cancel}, req.WithContext(ctx))
							return w.Code
						}
						for _, missing := range route.scopes {
							key.Permissions = nil
							for _, grant := range config.AllPermissions {
								probe := config.APIKeyConfig{Permissions: []string{grant}}
								if !probe.HasPermission(missing) {
									key.Permissions = append(key.Permissions, grant)
								}
							}
							if status := request(); status != http.StatusForbidden {
								t.Errorf("without %s, retaining all other permissions: status %d, want 403", missing, status)
							}
						}
						key.Permissions = route.scopes
						if status := request(); status == http.StatusUnauthorized || status == http.StatusForbidden || status >= 500 {
							t.Errorf("with exact scopes %v: unexpected status %d", route.scopes, status)
						}
					})
				}
			})
		}
	}
}

func TestBulkActionPermissions(t *testing.T) {
	srv, _ := regressionServer(t, nil)
	t.Cleanup(srv.Close)
	cookie := webLoginCookie(t, srv, regressionAdminKey)
	key := &srv.cfg.Auth.APIKeys[0]
	key.SuperAdmin = false
	for _, web := range []bool{false, true} {
		for _, action := range []string{"pause", "resume", "delete", "set_group"} {
			t.Run(fmt.Sprintf("web=%t/%s", web, action), func(t *testing.T) {
				ctx := context.Background()
				var groupID int64
				if action == "set_group" {
					group := &storage.MonitorGroup{Name: fmt.Sprintf("bulk group %t", web)}
					if err := srv.store.CreateMonitorGroup(ctx, group); err != nil {
						t.Fatal(err)
					}
					groupID = group.ID
				}
				m := &storage.Monitor{Name: "bulk boundary", Type: "manual", Target: "manual", Enabled: action != "resume", Interval: 60, Timeout: 10}
				if err := srv.store.CreateMonitor(ctx, m); err != nil {
					t.Fatal(err)
				}
				request := func(origin string) int {
					path, body, contentType := "/api/v1/monitors/bulk", fmt.Sprintf(`{"action":%q,"ids":[%d],"group_id":%d}`, action, m.ID, groupID), "application/json"
					if web {
						path, body, contentType = "/monitors/bulk", url.Values{"action": {action}, "ids[]": {fmt.Sprint(m.ID)}, "group_id": {fmt.Sprint(groupID)}}.Encode(), "application/x-www-form-urlencoded"
					}
					req := httptest.NewRequest("POST", path, strings.NewReader(body))
					req.Header.Set("Content-Type", contentType)
					if web {
						req.AddCookie(cookie)
						req.Header.Set("Origin", origin)
					} else {
						req.Header.Set("X-API-Key", regressionAdminKey)
					}
					w := httptest.NewRecorder()
					srv.ServeHTTP(w, req)
					return w.Code
				}
				key.Permissions = []string{"monitors.read", "monitors.update", "incidents.write", "groups.write"}
				if got := request("http://example.com"); got != http.StatusForbidden {
					t.Fatalf("unrelated grants: %d", got)
				}
				unchanged, err := srv.store.GetMonitor(ctx, m.ID)
				if err != nil || unchanged.Enabled != m.Enabled || unchanged.GroupID != nil {
					t.Fatalf("denied action changed monitor: %+v, %v", unchanged, err)
				}
				key.Permissions = []string{"monitors." + action}
				if action == "set_group" {
					key.Permissions = []string{"monitors.assign"}
					if got := request("http://example.com"); got != http.StatusForbidden {
						t.Fatalf("assignment without group visibility: %d", got)
					}
					key.Permissions = append(key.Permissions, "groups.read")
				}
				if web && request("https://untrusted.invalid") != http.StatusForbidden {
					t.Fatal("authorized bulk action accepted an invalid origin")
				}
				want := http.StatusOK
				if web {
					want = http.StatusSeeOther
				}
				if got := request("http://example.com"); got != want {
					t.Fatalf("exact action grant: %d, want %d", got, want)
				}
				updated, err := srv.store.GetMonitor(ctx, m.ID)
				if action == "delete" {
					if err == nil {
						t.Fatal("authorized delete did not remove monitor")
					}
				} else if err != nil || (action == "pause" && updated.Enabled) || (action == "resume" && !updated.Enabled) {
					t.Fatalf("authorized action did not take effect: %+v, %v", updated, err)
				} else if action == "set_group" && (updated.GroupID == nil || *updated.GroupID != groupID) {
					t.Fatalf("authorized group assignment did not take effect: %+v", updated.GroupID)
				}
			})
		}
	}
}

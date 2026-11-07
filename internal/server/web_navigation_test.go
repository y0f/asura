package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/y0f/asura/internal/config"
)

func TestWebNavigationPermissions(t *testing.T) {
	monitorPages := []string{"/", "/monitors"}
	allPages := append(slices.Clone(monitorPages), "/groups", "/tags", "/sla", "/status-pages", "/agents", "/proxies", "/incidents", "/notifications", "/notifications/history", "/escalation-policies", "/on-call", "/maintenance", "/logs", "/audit", "/settings")
	for _, basePath := range []string{"", "/asura"} {
		for _, tc := range []struct {
			name, landing string
			permissions   []string
			superAdmin    bool
			pages, extra  []string
		}{
			{name: "monitors", landing: "/", permissions: []string{"monitors.read"}, pages: monitorPages},
			{name: "monitor editor", landing: "/", permissions: []string{"monitors.read", "monitors.write"}, pages: monitorPages, extra: []string{"/monitors/new"}},
			{name: "incidents", landing: "/incidents", permissions: []string{"incidents.read"}, pages: []string{"/incidents"}},
			{name: "notifications", landing: "/notifications", permissions: []string{"notifications.read"}, pages: []string{"/notifications"}},
			{name: "escalation", landing: "/escalation-policies", permissions: []string{"escalation_policies.read"}, pages: []string{"/escalation-policies"}},
			{name: "maintenance", landing: "/maintenance", permissions: []string{"maintenance.read"}, pages: []string{"/maintenance"}},
			{name: "groups", landing: "/groups", permissions: []string{"groups.read"}, pages: []string{"/groups"}},
			{name: "tags", landing: "/tags", permissions: []string{"tags.read"}, pages: []string{"/tags"}},
			{name: "SLA", landing: "/sla", permissions: []string{"sla.read"}, pages: []string{"/sla"}},
			{name: "status pages", landing: "/status-pages", permissions: []string{"status_pages.read"}, pages: []string{"/status-pages"}},
			{name: "agents", landing: "/agents", permissions: []string{"agents.read"}, pages: []string{"/agents"}},
			{name: "proxies", landing: "/proxies", permissions: []string{"proxies.read"}, pages: []string{"/proxies"}},
			{name: "on-call", landing: "/on-call", permissions: []string{"on_call.read"}, pages: []string{"/on-call"}},
			{name: "history", landing: "/notifications/history", permissions: []string{"notification_history.read"}, pages: []string{"/notifications/history"}},
			{name: "logs", landing: "/logs", permissions: []string{"request_logs.read"}, pages: []string{"/logs"}},
			{name: "audit", landing: "/audit", permissions: []string{"audit.read"}, pages: []string{"/audit"}},
			{name: "settings", landing: "/settings", permissions: []string{"settings.read"}, pages: []string{"/settings"}},
			{name: "API metrics only", permissions: []string{"metrics.read"}},
			{name: "mixed", landing: "/incidents", permissions: []string{"incidents.read", "audit.read"}, pages: []string{"/incidents", "/audit"}},
			{name: "admin", landing: "/", superAdmin: true, pages: allPages, extra: []string{"/monitors/new"}},
			{name: "write only", permissions: []string{"monitors.write"}},
		} {
			t.Run(basePath+"/"+tc.name, func(t *testing.T) {
				srv, _ := regressionServer(t, func(cfg *config.Config) {
					cfg.Server.BasePath = basePath
					cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "navigation", Hash: config.HashAPIKey("navigation-key"), Permissions: tc.permissions, SuperAdmin: tc.superAdmin}}
				})
				t.Cleanup(srv.Close)
				form := url.Values{"api_key": {"navigation-key"}}
				req := httptest.NewRequest(http.MethodPost, basePath+"/login", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				login := httptest.NewRecorder()
				srv.ServeHTTP(login, req)
				if tc.landing == "" {
					if login.Code != http.StatusOK || len(login.Result().Cookies()) != 0 || !strings.Contains(login.Body.String(), "does not have access to any application pages") {
						t.Fatalf("key without read permissions should receive an actionable login message without a session; status=%d", login.Code)
					}
					return
				}
				if got := login.Header().Get("Location"); login.Code != http.StatusSeeOther || got != basePath+tc.landing {
					t.Fatalf("login destination = %d %q, want 303 %q", login.Code, got, basePath+tc.landing)
				}
				if len(login.Result().Cookies()) != 1 {
					t.Fatal("login did not create one session cookie")
				}
				homeRequest := httptest.NewRequest(http.MethodGet, basePath+"/", nil)
				homeRequest.AddCookie(login.Result().Cookies()[0])
				home := httptest.NewRecorder()
				srv.ServeHTTP(home, homeRequest)
				if tc.landing == "/" {
					if home.Code != http.StatusOK {
						t.Fatalf("home status = %d, want 200", home.Code)
					}
				} else if home.Code != http.StatusSeeOther || home.Header().Get("Location") != basePath+tc.landing {
					t.Fatalf("home destination = %d %q, want 303 %q", home.Code, home.Header().Get("Location"), basePath+tc.landing)
				}
				req = httptest.NewRequest(http.MethodGet, basePath+tc.landing, nil)
				req.AddCookie(login.Result().Cookies()[0])
				page := httptest.NewRecorder()
				srv.ServeHTTP(page, req)
				if page.Code != http.StatusOK {
					t.Fatalf("landing page status = %d, want 200", page.Code)
				}
				doc, err := html.Parse(strings.NewReader(page.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				var sidebar, palette []string
				var walk func(*html.Node)
				walk = func(node *html.Node) {
					attrs := map[string]string{}
					for _, a := range node.Attr {
						attrs[a.Key] = a.Val
					}
					if node.Data == "a" && slices.Contains(strings.Fields(attrs["class"]), "nav-link") {
						sidebar = append(sidebar, attrs["href"])
					}
					if attrs["aria-label"] == "Asura home" && attrs["href"] != basePath+tc.landing {
						t.Errorf("home link = %q, want %q", attrs["href"], basePath+tc.landing)
					}
					if raw, ok := attrs["data-nav"]; ok {
						var entries []struct{ Href string }
						if err := json.Unmarshal([]byte(raw), &entries); err != nil {
							t.Errorf("parse palette entries: %v", err)
						}
						for _, entry := range entries {
							palette = append(palette, entry.Href)
						}
					}
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						walk(child)
					}
				}
				walk(doc)
				for name, actual := range map[string][]string{"sidebar": sidebar, "palette": palette} {
					want := slices.Clone(tc.pages)
					if name == "palette" {
						want = append(want, tc.extra...)
					}
					for i := range want {
						want[i] = basePath + want[i]
					}
					slices.Sort(actual)
					slices.Sort(want)
					if !slices.Equal(actual, want) {
						t.Errorf("%s links = %v, want %v", name, actual, want)
					}
				}
			})
		}
	}
}

func TestWebHomeWithoutReadablePermission(t *testing.T) {
	srv, _ := regressionServer(t, nil)
	t.Cleanup(srv.Close)
	cookie := webLoginCookie(t, srv, regressionAdminKey)
	srv.cfg.Auth.APIKeys[0].SuperAdmin = false
	srv.cfg.Auth.APIKeys[0].Permissions = []string{"monitors.write"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("home after read permission removal = %d, want 403", w.Code)
	}
}

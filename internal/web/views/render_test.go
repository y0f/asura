package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/y0f/asura/internal/sla"
	"github.com/y0f/asura/internal/storage"
)

// These tests render every page with small fixtures and check structural
// invariants that are easy to regress: exactly one <h1>, every form control has
// an accessible name, every button and link has a name, and no password input
// carries a value.

func testLayout(title string, write bool) LayoutParams {
	perms := map[string]bool{}
	if write {
		for _, p := range []string{"monitors.write", "incidents.write", "notifications.write", "maintenance.write", "escalation_policies.write"} {
			perms[p] = true
		}
	}
	return LayoutParams{Title: title, Username: "admin", Perms: perms, Version: "test"}
}

func fixtureMonitor() *storage.Monitor {
	now := time.Now()
	return &storage.Monitor{ID: 1, Name: "API", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, Status: "up", LastCheckAt: &now, FailureThreshold: 3, SuccessThreshold: 1}
}

func fixtureIncident() *storage.Incident {
	return &storage.Incident{ID: 1, MonitorID: 1, MonitorName: "API", Status: "open", Severity: "critical", Cause: "timeout", StartedAt: time.Now()}
}

func pages(write bool) map[string]templ.Component {
	m := fixtureMonitor()
	inc := fixtureIncident()
	ch := &storage.NotificationChannel{ID: 1, Name: "Ops", Type: "slack", Enabled: true, Events: []string{"incident.created"}}
	paged := func(data any) *storage.PaginatedResult {
		return &storage.PaginatedResult{Data: data, Total: 1, Page: 1, PerPage: 20, TotalPages: 2}
	}
	lp := func(t string) LayoutParams { return testLayout(t, write) }
	return map[string]templ.Component{
		"dashboard":      DashboardPage(DashboardParams{LayoutParams: lp("Dashboard"), Monitors: []*storage.Monitor{m}, Incidents: []*storage.Incident{inc}, Total: 1, Up: 1, OpenIncidents: 1, Page: 1, TotalPages: 1}),
		"monitors":       MonitorListPage(MonitorListParams{LayoutParams: lp("Monitors"), Result: paged([]*storage.Monitor{m}), Groups: []*storage.MonitorGroup{{ID: 1, Name: "Prod"}}, AllTags: []*storage.Tag{{ID: 1, Name: "prod", Color: "#fff"}}}),
		"monitor-detail": MonitorDetailPage(MonitorDetailParams{LayoutParams: lp("API"), Monitor: m, Checks: paged([]*storage.CheckResult{{Status: "up", CreatedAt: time.Now()}}), SLA: &SLAData{Target: 99.9, BudgetTotalSecs: 100, BudgetRemainSecs: 50}}),
		"monitor-form":   MonitorFormPage(MonitorFormParams{LayoutParams: lp("New Monitor"), Monitor: &storage.Monitor{}, HeadersJSON: "[]", WsHeadersJSON: "[]", AssertionsJSON: `{"operator":"and","groups":[]}`, NotificationChannels: []*storage.NotificationChannel{ch}, AllTags: []*storage.Tag{{ID: 1, Name: "prod", Color: "#fff"}}}),
		"incidents":      IncidentListPage(IncidentListParams{LayoutParams: lp("Incidents"), Result: paged([]*storage.Incident{inc})}),
		"incident":       IncidentDetailPage(IncidentDetailParams{LayoutParams: lp("Incident #1"), Incident: inc, Events: []*storage.IncidentEvent{{Type: "created", Message: "opened", CreatedAt: time.Now()}}}),
		"groups":         GroupListPage(GroupListParams{LayoutParams: lp("Groups"), Groups: []*storage.MonitorGroup{{ID: 1, Name: "Prod"}}}),
		"group":          GroupDetailPage(GroupDetailParams{LayoutParams: lp("Prod"), Group: &storage.MonitorGroup{ID: 1, Name: "Prod"}, Monitors: paged([]*storage.Monitor{m})}),
		"tags":           TagListPage(TagListParams{LayoutParams: lp("Tags"), Tags: []*storage.Tag{{ID: 1, Name: "prod", Color: "#fff"}}}),
		"notifications":  NotificationListPage(NotificationListParams{LayoutParams: lp("Notifications"), Channels: []*storage.NotificationChannel{ch}}),
		"notif-history":  NotificationHistoryPage(NotificationHistoryParams{LayoutParams: lp("Notification History"), Channels: []*storage.NotificationChannel{ch}, Result: paged([]*storage.NotificationHistory{{ChannelName: "Ops", Status: "sent", EventType: "test", SentAt: time.Now()}})}),
		"escalation":     EscalationPolicyListPage(EscalationPolicyListParams{LayoutParams: lp("Escalation Policies"), Channels: []*storage.NotificationChannel{ch}, Policies: []*storage.EscalationPolicy{{ID: 1, Name: "Default", Enabled: true}}}),
		"oncall":         OnCallListPage(OnCallListParams{LayoutParams: lp("On-Call"), Channels: []*storage.NotificationChannel{ch}, Rotations: []*storage.OnCallRotation{{ID: 1, Name: "Primary", Period: "weekly", ChannelIDs: []int64{1}}}}),
		"maintenance":    MaintenanceListPage(MaintenanceListParams{LayoutParams: lp("Maintenance"), Windows: []*storage.MaintenanceWindow{{ID: 1, Name: "Upgrade", StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)}}}),
		"sla":            SLAReportPage(SLAReportParams{LayoutParams: lp("SLA Report"), Period: time.Now(), Entries: []*sla.ReportEntry{{MonitorID: 1, MonitorName: "API", Target: 99.9, UptimePct: 99.95, BudgetSecs: 100, RemainSecs: 60}}}),
		"logs":           RequestLogListPage(RequestLogParams{LayoutParams: lp("Request Logs"), TimeRange: "24h", TopIPs: []string{"127.0.0.1"}, Stats: &storage.RequestLogStats{}, Result: paged([]*storage.RequestLog{{Method: "GET", Path: "/", StatusCode: 200, ClientIP: "127.0.0.1", RouteGroup: "web", CreatedAt: time.Now()}})}),
		"audit":          AuditLogPage(AuditLogParams{LayoutParams: lp("Audit Log"), TimeRange: "24h", Result: paged([]*storage.AuditEntry{{Action: "create", Entity: "monitor", EntityID: 1, CreatedAt: time.Now()}})}),
		"agents":         AgentListPage(AgentListParams{LayoutParams: lp("Agents"), ServerURL: "http://localhost", Agents: []*storage.Agent{{ID: 1, Name: "eu"}}, NewAgentName: "eu", NewAgentToken: "tok"}),
		"proxies":        ProxyListPage(ProxyListParams{LayoutParams: lp("Proxies"), Proxies: []*storage.Proxy{{ID: 1, Name: "p", Protocol: "http", Host: "h", Port: 8080}}}),
		"proxy-form":     ProxyFormPage(ProxyFormParams{LayoutParams: lp("Edit Proxy"), Proxy: &storage.Proxy{ID: 1, Name: "p", AuthUser: "u"}, PasswordStored: true}),
		"status-pages":   StatusPageListPage(StatusPageListParams{LayoutParams: lp("Status Pages"), Pages: []*storage.StatusPage{{ID: 1, Title: "Status", Slug: "status", Enabled: true}}}),
		"status-form":    StatusPageFormPage(StatusPageFormParams{LayoutParams: lp("Status Page"), Monitors: []*storage.Monitor{m}, Assigned: map[int64]bool{}, AssignedData: map[int64]storage.StatusPageMonitor{}}),
		"settings":       SettingsPage(SettingsParams{LayoutParams: lp("Settings")}),
		"login":          LoginPage(LoginParams{Error: "bad key"}),
		"totp":           TOTPPage(TOTPParams{}),
		"public":         PublicStatusPage(PublicStatusPageParams{Title: "Acme", Config: &storage.StatusPage{Slug: "status", ShowIncidents: true}, Overall: "operational", Monitors: []MonitorWithUptime{{Monitor: m, UptimeLabel: "100%"}}, Incidents: []*storage.Incident{inc}, HasIncidents: true, SubscriptionsEnabled: true}),
		"public-auth":    StatusPageAuthPage(StatusPageAuthParams{Title: "Acme", Slug: "status"}),
	}
}

func render(t *testing.T, c templ.Component) *html.Node {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	doc, err := html.Parse(&buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

func hasAncestor(n *html.Node, tag string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == tag {
			return true
		}
	}
	return false
}

// accessibleName reports whether an element has a name from aria attributes,
// Alpine-bound aria/text attributes, a title, or its text content.
func accessibleName(n *html.Node) bool {
	for _, k := range []string{"aria-label", "aria-labelledby", ":aria-label", "title", ":title", "x-text"} {
		if v, ok := attr(n, k); ok && v != "" {
			return true
		}
	}
	return textOf(n) != ""
}

func TestPagesStructure(t *testing.T) {
	for _, write := range []bool{true, false} {
		for name, page := range pages(write) {
			doc := render(t, page)
			labelled := map[string]bool{}
			var h1s int
			var problems []string
			var collect func(*html.Node)
			collect = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "label" {
					if f, ok := attr(n, "for"); ok {
						labelled[f] = true
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					collect(c)
				}
			}
			collect(doc)
			var check func(*html.Node)
			check = func(n *html.Node) {
				// Alpine <template> clones are checked through their x-text/aria
				// bindings at runtime, not as static markup.
				if n.Type == html.ElementNode && n.Data == "template" {
					return
				}
				if n.Type == html.ElementNode {
					switch n.Data {
					case "h1":
						h1s++
					case "input", "select", "textarea":
						typ, _ := attr(n, "type")
						if typ == "hidden" {
							break
						}
						id, _ := attr(n, "id")
						if !labelled[id] && !hasAncestor(n, "label") && !accessibleName(n) {
							nm, _ := attr(n, "name")
							problems = append(problems, "unlabelled "+n.Data+" name="+nm)
						}
						if typ == "password" {
							if v, ok := attr(n, "value"); ok && v != "" {
								problems = append(problems, "password input carries a value")
							}
						}
					case "button", "a":
						if !accessibleName(n) {
							cls, _ := attr(n, "class")
							problems = append(problems, "unnamed "+n.Data+" class="+cls)
						}
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					check(c)
				}
			}
			check(doc)
			if h1s != 1 {
				problems = append(problems, "expected exactly one <h1>")
			}
			for _, p := range problems {
				t.Errorf("%s (write=%v): %s", name, write, p)
			}
		}
	}
}

func TestSettingsExportLinks(t *testing.T) {
	var buf bytes.Buffer
	if err := SettingsPage(SettingsParams{LayoutParams: testLayout("Settings", true)}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "/settings/export?redact_secrets=false") || !strings.Contains(out, "/settings/export?redact_secrets=true") {
		t.Fatal("export buttons do not request explicit redaction modes")
	}
}

func TestReadOnlyPagesHideWriteActions(t *testing.T) {
	for name, page := range pages(false) {
		if name == "login" || name == "totp" || name == "public" || name == "public-auth" || name == "monitor-form" || name == "proxy-form" || name == "status-form" {
			continue
		}
		var buf bytes.Buffer
		if err := page.Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		for _, marker := range []string{"/delete\"", "/monitors/new\"", "/status-pages/new\"", "/settings/import\""} {
			if strings.Contains(out, marker) {
				t.Errorf("%s: read-only page links to write action %s", name, marker)
			}
		}
	}
}

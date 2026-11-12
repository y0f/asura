package views

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/y0f/asura/v3/internal/sla"
	"github.com/y0f/asura/v3/internal/storage"
	"golang.org/x/net/html"
)

func namedControl(doc *html.Node, name string) bool {
	if doc.Type == html.ElementNode && (doc.Data == "button" || doc.Data == "a") {
		label, _ := attr(doc, "aria-label")
		if label == "" {
			label = strings.TrimSpace(textOf(doc))
		}
		if label == name {
			return true
		}
	}
	for child := doc.FirstChild; child != nil; child = child.NextSibling {
		if namedControl(child, name) {
			return true
		}
	}
	return false
}

func TestGlobalActionsUseIndependentGrants(t *testing.T) {
	type action struct{ scope, label string }
	tests := []struct {
		resource string
		page     func(LayoutParams) templ.Component
		actions  []action
	}{
		{"agents", func(p LayoutParams) templ.Component {
			return AgentListPage(AgentListParams{LayoutParams: p, Agents: []*storage.Agent{{ID: 1, Name: "Agent"}}})
		}, []action{{"create", "New agent"}, {"delete", "Delete"}}},
		{"groups", func(p LayoutParams) templ.Component {
			return GroupListPage(GroupListParams{LayoutParams: p, Groups: []*storage.MonitorGroup{{ID: 1, Name: "Group"}}})
		}, []action{{"create", "New group"}, {"update", "Edit Group"}, {"delete", "Delete"}}},
		{"tags", func(p LayoutParams) templ.Component {
			return TagListPage(TagListParams{LayoutParams: p, Tags: []*storage.Tag{{ID: 1, Name: "Tag"}}})
		}, []action{{"create", "New tag"}, {"update", "Edit Tag"}, {"delete", "Delete"}}},
		{"proxies", func(p LayoutParams) templ.Component {
			return ProxyListPage(ProxyListParams{LayoutParams: p, Proxies: []*storage.Proxy{{ID: 1, Name: "Proxy"}}})
		}, []action{{"create", "New proxy"}, {"update", "Edit Proxy"}, {"delete", "Delete"}}},
		{"status_pages", func(p LayoutParams) templ.Component {
			return StatusPageListPage(StatusPageListParams{LayoutParams: p, Pages: []*storage.StatusPage{{ID: 1, Title: "Status"}}})
		}, []action{{"create", "New status page"}, {"update", "Edit"}, {"delete", "Delete"}}},
		{"notifications", func(p LayoutParams) templ.Component {
			return NotificationListPage(NotificationListParams{LayoutParams: p, Channels: []*storage.NotificationChannel{{ID: 1, Name: "Channel", Type: "webhook"}}})
		}, []action{{"create", "New channel"}, {"update", "Edit"}, {"test", "Send test"}, {"delete", "Delete"}}},
		{"escalation_policies", func(p LayoutParams) templ.Component {
			return EscalationPolicyListPage(EscalationPolicyListParams{LayoutParams: p, Policies: []*storage.EscalationPolicy{{ID: 1, Name: "Policy"}}})
		}, []action{{"create", "New policy"}, {"update", "Edit Policy"}, {"delete", "Delete"}}},
		{"on_call", func(p LayoutParams) templ.Component {
			return OnCallListPage(OnCallListParams{LayoutParams: p, Rotations: []*storage.OnCallRotation{{ID: 1, Name: "Rotation"}}})
		}, []action{{"create", "New rotation"}, {"override", "Override"}, {"delete", "Delete"}}},
	}
	for _, tc := range tests {
		for _, grant := range append([]action{{scope: "read"}}, tc.actions...) {
			t.Run(tc.resource+"/"+grant.scope, func(t *testing.T) {
				p := LayoutParams{Title: tc.resource, Perms: map[string]bool{tc.resource + ".read": true, tc.resource + "." + grant.scope: true}}
				if tc.resource == "on_call" || tc.resource == "escalation_policies" {
					p.Perms["notifications.read"] = true
				}
				doc := render(t, tc.page(p))
				for _, candidate := range tc.actions {
					if got, want := namedControl(doc, candidate.label), candidate.scope == grant.scope; got != want {
						t.Errorf("grant %s: %q visible=%v, want %v", grant.scope, candidate.label, got, want)
					}
				}
			})
		}
	}
}

func TestEscalationChannelLabelsAndStepEditorNeedChannelRead(t *testing.T) {
	for _, canUpdate := range []bool{false, true} {
		p := EscalationPolicyListParams{
			LayoutParams: LayoutParams{Perms: map[string]bool{"escalation_policies.read": true, "escalation_policies.create": true, "escalation_policies.update": canUpdate}},
			Policies:     []*storage.EscalationPolicy{{ID: 1, Name: "Policy", Steps: []*storage.EscalationPolicyStep{{NotificationChannelIDs: []int64{7}}}}},
			Channels:     []*storage.NotificationChannel{{ID: 7, Name: "hidden-channel"}},
		}
		doc := render(t, EscalationPolicyListPage(p))
		if strings.Contains(textOf(doc), "hidden-channel") || !strings.Contains(textOf(doc), "Channel #7") || namedControl(doc, "Add step") || namedControl(doc, "New policy") {
			t.Fatalf("update=%v: related channel metadata or mutation was exposed", canUpdate)
		}
		if namedControl(doc, "Edit Policy") != canUpdate {
			t.Fatalf("update=%v: ordinary policy metadata edit was gated incorrectly", canUpdate)
		}
	}
}

func TestOnCallAssignmentNeedsChannelReadButClearingDoesNot(t *testing.T) {
	p := OnCallListParams{
		LayoutParams: LayoutParams{Perms: map[string]bool{"on_call.read": true, "on_call.create": true, "on_call.override": true}},
		Rotations:    []*storage.OnCallRotation{{ID: 1, Name: "Primary", ChannelIDs: []int64{7}}},
		Channels:     []*storage.NotificationChannel{{ID: 7, Name: "hidden-channel"}},
	}
	doc := render(t, OnCallListPage(p))
	if namedControl(doc, "New rotation") || namedControl(doc, "Override") || !namedControl(doc, "Clear override") || strings.Contains(textOf(doc), "hidden-channel") {
		t.Fatal("channel assignment/name leaked, or clearing was hidden")
	}
	p.Perms["notifications.read"] = true
	doc = render(t, OnCallListPage(p))
	if !namedControl(doc, "New rotation") || !namedControl(doc, "Override") || !strings.Contains(textOf(doc), "hidden-channel") {
		t.Fatal("authorized channel assignment is unavailable")
	}
}

// A deliberately narrow test identity ensures views do not substitute a
// capability somewhere for permission to operate on a particular row.
type testAccess struct {
	capabilities map[string]bool
	monitors     map[string]map[int64]bool
	groups       map[string]map[int64]bool
}

func (a testAccess) HasCapability(action string) bool { return a.capabilities[action] }
func (a testAccess) AllowsMonitor(action string, monitorID int64, groupID *int64) bool {
	return a.monitors[action][monitorID] || (groupID != nil && a.groups[action][*groupID])
}

func TestMonitorFormSeparatesCreationAndMoveDestinations(t *testing.T) {
	groupA, groupB, groupC := int64(10), int64(20), int64(30)
	access := testAccess{
		capabilities: map[string]bool{"monitors.create": true},
		groups:       map[string]map[int64]bool{"monitors.create": {groupA: true}},
	}
	p := MonitorFormParams{LayoutParams: LayoutParams{Access: access}, Monitor: &storage.Monitor{}}
	if !p.canAssign("groups") || !p.canUseGroup(&groupA) || p.canUseGroup(&groupB) || p.canUseGroup(nil) {
		t.Fatal("scoped creation needs its allowed group but not global group metadata access")
	}
	access.monitors = map[string]map[int64]bool{"monitors.update": {1: true}, "monitors.assign": {1: true}}
	access.groups = map[string]map[int64]bool{"monitors.update": {groupB: true}, "monitors.assign": {groupC: true}}
	p.Access, p.Perms = access, map[string]bool{"groups.read": true}
	p.Monitor = &storage.Monitor{ID: 1, GroupID: &groupA}
	if !p.canUseGroup(&groupA) || p.canUseGroup(&groupB) || p.canUseGroup(&groupC) || p.canUseGroup(nil) {
		t.Fatal("unchanged group must remain available, without mixing destination grants")
	}
	access.groups["monitors.assign"][groupB] = true
	if !p.canUseGroup(&groupB) {
		t.Fatal("destination with both update and assign authority should be available")
	}
	delete(p.Perms, "groups.read")
	if p.canAssign("groups") || p.canUseGroup(&groupB) {
		t.Fatal("group metadata read is required when moving an existing monitor")
	}
}

func TestScopedRowsAndBulkPermissionsStaySeparate(t *testing.T) {
	access := testAccess{
		capabilities: map[string]bool{"monitors.read": true, "monitors.pause": true, "monitors.delete": true},
		monitors: map[string]map[int64]bool{
			"monitors.read": {1: true, 2: true}, "monitors.pause": {1: true}, "monitors.delete": {2: true},
		},
	}
	p := LayoutParams{Access: access}
	first := &storage.Monitor{ID: 1, Name: "First", Enabled: true}
	second := &storage.Monitor{ID: 2, Name: "Second", Enabled: true}
	for _, monitor := range []*storage.Monitor{first, second} {
		doc := render(t, MonitorRowActions(p, monitor))
		if got := namedControl(doc, "Pause "+monitor.Name); got != (monitor.ID == 1) {
			t.Errorf("monitor %d has unauthorized or missing Pause action", monitor.ID)
		}
		if namedControl(doc, "Edit "+monitor.Name) {
			t.Errorf("monitor %d unexpectedly exposes Edit", monitor.ID)
		}
	}
	list := MonitorListParams{LayoutParams: p, Result: &storage.PaginatedResult{Data: []*storage.Monitor{first, second}}}
	var allowed map[string][]int64
	if err := json.Unmarshal([]byte(list.bulkPermissions()), &allowed); err != nil {
		t.Fatal(err)
	}
	if len(allowed["pause"]) != 1 || allowed["pause"][0] != 1 || len(allowed["delete"]) != 1 || allowed["delete"][0] != 2 || len(allowed["assign"]) != 0 {
		t.Fatalf("bulk permissions expanded or mixed independent grants: %v", allowed)
	}
	if p.HomePath() != "/" || !p.CanVisit("/monitors") || p.Can("monitors.read") {
		t.Fatal("scoped navigation must work without claiming global monitor access")
	}
}

func TestMaintenanceRequiresFullWindowAuthority(t *testing.T) {
	p := MaintenanceListParams{AllowedActions: map[int64]map[string]bool{1: {"maintenance.toggle": true}}}
	if !p.CanMaintenance("maintenance.toggle", &storage.MaintenanceWindow{ID: 1}) || p.CanMaintenance("maintenance.delete", &storage.MaintenanceWindow{ID: 1}) || p.CanMaintenance("maintenance.toggle", &storage.MaintenanceWindow{ID: 2}) {
		t.Fatal("window authority leaked between actions or windows")
	}
	p.Perms = map[string]bool{"maintenance.delete": true}
	if !p.CanMaintenance("maintenance.delete", &storage.MaintenanceWindow{ID: 2}) {
		t.Fatal("global window permission was lost")
	}
}

func TestRelationSelectorsDistinguishOmittedAndClearedAssignments(t *testing.T) {
	for _, assignment := range []bool{false, true} {
		for _, relatedRead := range []bool{false, true} {
			p := MonitorFormParams{
				LayoutParams: LayoutParams{
					Perms:  map[string]bool{"notifications.read": relatedRead, "tags.read": relatedRead, "agents.read": relatedRead},
					Access: testAccess{monitors: map[string]map[int64]bool{"monitors.assign": {1: assignment}}},
				},
				Monitor:              fixtureMonitor(),
				NotificationChannels: []*storage.NotificationChannel{{ID: 1, Name: "Channel"}},
			}
			doc := render(t, MonitorFormPage(p))
			markers := map[string]bool{}
			var collect func(*html.Node)
			collect = func(node *html.Node) {
				if name, _ := attr(node, "name"); name == "relation_fields" {
					value, _ := attr(node, "value")
					markers[value] = true
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					collect(child)
				}
			}
			collect(doc)
			for _, relation := range []string{"notifications", "tags", "agents"} {
				if markers[relation] != (assignment && relatedRead) {
					t.Errorf("assign=%v read=%v relation=%s: unexpected submitted marker", assignment, relatedRead, relation)
				}
			}
		}
	}
}

func TestCloneRequiresReadableSourceAndDestinationAuthority(t *testing.T) {
	groupID := int64(10)
	access := testAccess{monitors: map[string]map[int64]bool{
		"monitors.read": {1: true}, "monitors.clone": {1: true},
	}, groups: map[string]map[int64]bool{"monitors.clone": {}}}
	p := MonitorDetailParams{LayoutParams: LayoutParams{Access: access}, Monitor: &storage.Monitor{ID: 1, Name: "Source", Type: "manual", GroupID: &groupID}, CloneAllowed: true}
	if namedControl(render(t, MonitorDetailPage(p)), "Clone") {
		t.Fatal("an ID-only clone grant cannot create another monitor")
	}
	access.groups["monitors.clone"][groupID] = true
	if !namedControl(render(t, MonitorDetailPage(p)), "Clone") {
		t.Fatal("readable source and authorized destination should expose Clone")
	}
	p.CloneAllowed = false
	if namedControl(render(t, MonitorDetailPage(p)), "Clone") {
		t.Fatal("clone must also require the handler's copied-relationship authorization")
	}
	p.CloneAllowed = true
	delete(access.monitors["monitors.read"], 1)
	if namedControl(render(t, MonitorDetailPage(p)), "Clone") {
		t.Fatal("clone must not expose an unreadable source")
	}
}

func TestAssociatedRecordsDoNotExposeUnreadableNamesOrLinks(t *testing.T) {
	monitorID, groupID := int64(1), int64(10)
	access := testAccess{monitors: map[string]map[int64]bool{"monitors.read": {}}, groups: map[string]map[int64]bool{}}
	layout := LayoutParams{Access: access, BasePath: "/asura"}
	incident := &storage.Incident{ID: 2, MonitorID: monitorID, MonitorGroupID: &groupID, MonitorName: "hidden-monitor", Status: "open", StartedAt: time.Now()}
	history := &storage.NotificationHistory{ChannelID: 3, ChannelName: "hidden-channel", ChannelType: "webhook", MonitorID: &monitorID, MonitorGroupID: &groupID, MonitorName: "hidden-monitor", Status: "sent"}
	for name, page := range map[string]templ.Component{
		"incident": IncidentDetailPage(IncidentDetailParams{LayoutParams: layout, Incident: incident}),
		"history":  NotificationHistoryPage(NotificationHistoryParams{LayoutParams: layout, Result: &storage.PaginatedResult{Data: []*storage.NotificationHistory{history}}}),
		"sla":      SLAReportPage(SLAReportParams{LayoutParams: layout, Period: time.Now(), Entries: []*sla.ReportEntry{{MonitorID: monitorID, MonitorName: "hidden-monitor"}}}),
	} {
		doc := render(t, page)
		content := textOf(doc)
		if strings.Contains(content, "hidden-monitor") || strings.Contains(content, "hidden-channel") || !strings.Contains(content, "Monitor #1") {
			t.Errorf("%s disclosed related names or omitted the neutral identifier", name)
		}
		if hasHref(doc, "/asura/monitors/1") {
			t.Errorf("%s exposes an unauthorized monitor link", name)
		}
	}
	access.monitors["monitors.read"][monitorID] = true
	if !hasHref(render(t, IncidentDetailPage(IncidentDetailParams{LayoutParams: layout, Incident: incident})), "/asura/monitors/1") {
		t.Fatal("a readable monitor should have a working incident link")
	}
	if !hasHref(render(t, SLAReportPage(SLAReportParams{LayoutParams: layout, Period: time.Now(), ReadableMonitors: map[int64]bool{monitorID: true}, Entries: []*sla.ReportEntry{{MonitorID: monitorID, MonitorName: "visible-monitor"}}})), "/asura/monitors/1") {
		t.Fatal("the handler's per-monitor SLA read map should expose the permitted link")
	}
}

func hasHref(node *html.Node, href string) bool {
	if node.Type == html.ElementNode && node.Data == "a" {
		if got, _ := attr(node, "href"); got == href {
			return true
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasHref(child, href) {
			return true
		}
	}
	return false
}

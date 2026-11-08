package views

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/y0f/asura/internal/storage"
	"golang.org/x/net/html"
)

func TestDiagnosticViewsIntersectOwnerReads(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		monitorRead, incidentRead bool
		diagnosticID              int64
	}{
		{"ordinary", true, true, 0},
		{"diagnostic only", false, false, 1},
		{"different owner", true, true, 2},
		{"monitor evidence", true, false, 1},
		{"incident evidence", false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lp := LayoutParams{Access: testAccess{monitors: map[string]map[int64]bool{
				"monitors.read": {1: tc.monitorRead}, "incidents.read": {1: tc.incidentRead},
				"diagnostics.read": {tc.diagnosticID: true},
			}}}
			incident := &storage.Incident{ID: 1, MonitorID: 1, Cause: "private-cause-marker"}
			monitor := MonitorDetailParams{
				LayoutParams: lp, Monitor: &storage.Monitor{ID: 1, Type: "dns"},
				LatestCheck: &storage.CheckResult{ObservationKind: storage.ObservationProbe, DNSRecords: `["private-dns-marker"]`},
				Checks:      &storage.PaginatedResult{Data: []*storage.CheckResult{{Message: "private-check-marker"}}},
				Changes:     &storage.PaginatedResult{Data: []*storage.ContentChange{{Diff: "private-diff-marker"}}},
			}
			pages := []struct {
				name      string
				component templ.Component
				allowed   bool
				markers   []string
			}{
				{"monitor", MonitorDetailPage(monitor), tc.monitorRead && tc.diagnosticID == 1, []string{"private-check-marker", "private-dns-marker", "private-diff-marker"}},
				{"incident", IncidentDetailPage(IncidentDetailParams{LayoutParams: lp, Incident: incident, Events: []*storage.IncidentEvent{{Type: "created", Message: "private-event-marker"}}}), tc.incidentRead && tc.diagnosticID == 1, []string{"private-cause-marker", "private-event-marker"}},
				{"incident row", IncidentRow(lp, incident), tc.incidentRead && tc.diagnosticID == 1, []string{"private-cause-marker"}},
				{"incident list", IncidentListPage(IncidentListParams{LayoutParams: lp, Result: &storage.PaginatedResult{Data: []*storage.Incident{incident}}}), tc.incidentRead && tc.diagnosticID == 1, []string{"private-cause-marker"}},
			}
			for _, page := range pages {
				var output strings.Builder
				if err := html.Render(&output, render(t, page.component)); err != nil {
					t.Fatal(err)
				}
				for _, marker := range page.markers {
					if got := strings.Contains(output.String(), marker); got != page.allowed {
						t.Errorf("%s %s visibility=%v, want%v", page.name, marker, got, page.allowed)
					}
				}
			}
		})
	}
}

func TestSavedAssertionControlsDistinguishHiddenAndEmpty(t *testing.T) {
	for _, stored := range []bool{false, true} {
		for _, diagnostics := range []bool{false, true} {
			p := MonitorFormParams{
				LayoutParams: LayoutParams{Perms: map[string]bool{"monitors.read": true, "diagnostics.read": diagnostics}},
				Monitor:      &storage.Monitor{ID: 1, Type: "http"}, SecretsStored: map[string]bool{"assertions": stored},
			}
			doc := render(t, monitorAssertions(p))
			text := textOf(doc)
			if got := strings.Contains(text, "Remove saved conditions"); got != stored {
				t.Errorf("stored=%v removal control=%v", stored, got)
			}
			if stored && strings.Contains(text, "No conditions.") {
				t.Fatal("saved rules falsely described as absent")
			}
			if stored && diagnostics && !strings.Contains(text, "Saved conditions are shown") {
				t.Fatal("visible-rule hint missing")
			}
			if stored && !diagnostics && !strings.Contains(text, "Saved conditions stay hidden") {
				t.Fatal("hidden-rule hint missing")
			}
		}
	}
}

func TestOpenIncidentDiagnosticCauseUsesCurrentOwnerGroup(t *testing.T) {
	group := int64(7)
	lp := LayoutParams{Access: testAccess{groups: map[string]map[int64]bool{
		"monitors.read": {group: true}, "incidents.read": {group: true}, "diagnostics.read": {group: true},
	}}}
	for _, ownerGroup := range []*int64{nil, &group} {
		p := MonitorDetailParams{
			LayoutParams: lp, Monitor: &storage.Monitor{ID: 1, Type: "http", GroupID: &group},
			OpenIncident: &storage.Incident{ID: 2, MonitorID: 1, MonitorGroupID: ownerGroup, Cause: "private-group-cause"},
		}
		if shown := strings.Contains(textOf(render(t, MonitorDetailPage(p))), "private-group-cause"); shown != (ownerGroup != nil) {
			t.Errorf("group-scoped open incident cause visibility=%v", shown)
		}
	}
}

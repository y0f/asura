package storage

import (
	"context"
	"errors"
	"testing"
)

func statusPageEdit(members *[]StatusPageMonitor, edit func(*StatusPage)) StatusPagePrepare {
	return func(current *StatusPage) (*StatusPage, *[]StatusPageMonitor, error) {
		next := StatusPage{Title: "Services", Slug: "services"}
		if current != nil {
			next = *current
		}
		if edit != nil {
			edit(&next)
		}
		return &next, members, nil
	}
}

func TestStatusPageAtomicMembershipAndScopedPublication(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	members := []StatusPageMonitor{{MonitorID: monitors[0].ID}, {MonitorID: monitors[1].ID}}
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	if _, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&members, nil), access); !errors.Is(err, ErrStatusPageMonitorAccess) {
		t.Fatalf("creation accepted hidden membership: %v", err)
	}
	pages, err := s.ListStatusPages(ctx)
	if err != nil || len(pages) != 0 {
		t.Fatalf("failed creation left a page: %v %v", pages, err)
	}
	sp, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&members, func(p *StatusPage) { p.PasswordHash = "saved-secret" }), MonitorAccess{All: true})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := saveStatusPageTest(s, ctx, sp.ID, statusPageEdit(nil, func(p *StatusPage) { p.Description = "metadata only" }), access)
	if err != nil || updated.PasswordHash != "saved-secret" {
		t.Fatalf("metadata save lost password or rejected: %+v %v", updated, err)
	}
	storedMembers, err := s.ListStatusPageMonitors(ctx, sp.ID)
	if err != nil || len(storedMembers) != 2 {
		t.Fatalf("omitted membership changed: %v %v", storedMembers, err)
	}
	visible, err := s.ListStatusPageMonitors(ctx, sp.ID, access)
	if err != nil || len(visible) != 1 || visible[0].MonitorID != monitors[0].ID {
		t.Fatalf("member read leaked IDs: %v %v", visible, err)
	}
	for name, edit := range map[string]func(*StatusPage){
		"enable":           func(p *StatusPage) { p.Enabled = true },
		"api":              func(p *StatusPage) { p.APIEnabled = true },
		"incidents":        func(p *StatusPage) { p.ShowIncidents = true },
		"slug":             func(p *StatusPage) { p.Slug = "new-url" },
		"remove password":  func(p *StatusPage) { p.PasswordHash = "" },
		"replace password": func(p *StatusPage) { p.PasswordHash = "replacement" },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := saveStatusPageTest(s, ctx, sp.ID, statusPageEdit(nil, edit), access); !errors.Is(err, ErrStatusPageMonitorAccess) {
				t.Fatalf("exposure expansion accepted: %v", err)
			}
		})
	}
	empty := []StatusPageMonitor{}
	if _, err := saveStatusPageTest(s, ctx, sp.ID, statusPageEdit(&empty, nil), access); !errors.Is(err, ErrStatusPageMonitorAccess) {
		t.Fatalf("replaced hidden membership: %v", err)
	}
	if _, err := saveStatusPageTest(s, ctx, sp.ID, statusPageEdit(&empty, nil), MonitorAccess{All: true}); err != nil {
		t.Fatal(err)
	}
	storedMembers, err = s.ListStatusPageMonitors(ctx, sp.ID)
	if err != nil || len(storedMembers) != 0 {
		t.Fatalf("explicit authorized clear failed: %v %v", storedMembers, err)
	}
}

func TestStatusPageFailureRollsBackMetadataAndMembership(t *testing.T) {
	s, _, monitors := accessFixture(t)
	ctx := context.Background()
	members := []StatusPageMonitor{{MonitorID: monitors[0].ID}}
	sp, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&members, nil), MonitorAccess{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.writeDB.Exec(`CREATE TRIGGER fail_page_member BEFORE INSERT ON status_page_monitors BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	members = []StatusPageMonitor{{MonitorID: monitors[1].ID}}
	result, err := saveStatusPageTest(s, ctx, sp.ID, statusPageEdit(&members, func(p *StatusPage) { p.Title = "changed"; p.PasswordHash = "changed" }), MonitorAccess{All: true})
	if err == nil || result != nil {
		t.Fatalf("failed write published output: %+v %v", result, err)
	}
	stored, err := s.GetStatusPage(ctx, sp.ID)
	if err != nil || stored.Title != sp.Title || stored.PasswordHash != sp.PasswordHash {
		t.Fatalf("partial metadata committed: %+v %v", stored, err)
	}
	storedMembers, err := s.ListStatusPageMonitors(ctx, sp.ID)
	if err != nil || len(storedMembers) != 1 || storedMembers[0].MonitorID != monitors[0].ID {
		t.Fatalf("partial membership committed: %v %v", storedMembers, err)
	}
	if _, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&members, func(p *StatusPage) { p.Slug = "new" }), MonitorAccess{All: true}); err == nil {
		t.Fatal("failed creation succeeded")
	}
	pages, err := s.ListStatusPages(ctx)
	if err != nil || len(pages) != 1 {
		t.Fatalf("failed creation leaked metadata: %v %v", pages, err)
	}
}

func TestStatusPageChecksCurrentMonitorGroupAndInvalidReferences(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	members := []StatusPageMonitor{{MonitorID: monitors[0].ID}}
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	sp, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&members, nil), access)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BulkSetMonitorGroup(ctx, []int64{monitors[0].ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := saveStatusPageTest(s, ctx, sp.ID, statusPageEdit(nil, func(p *StatusPage) { p.Enabled = true }), access); !errors.Is(err, ErrStatusPageMonitorAccess) {
		t.Fatalf("stale group grant admitted publication: %v", err)
	}
	for _, bad := range [][]StatusPageMonitor{{{MonitorID: 0}}, {{MonitorID: monitors[0].ID}, {MonitorID: monitors[0].ID}}, {{MonitorID: monitors[0].ID, SortOrder: -1}}} {
		if _, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&bad, func(p *StatusPage) { p.Slug = "invalid" }), MonitorAccess{All: true}); !errors.Is(err, ErrInvalidStatusPageMonitors) {
			t.Fatalf("invalid membership admitted: %v", err)
		}
	}
	missing := []StatusPageMonitor{{MonitorID: 999999}}
	if _, err := saveStatusPageTest(s, ctx, 0, statusPageEdit(&missing, func(p *StatusPage) { p.Slug = "missing" }), MonitorAccess{All: true}); !errors.Is(err, ErrStatusPageMonitorAccess) {
		t.Fatalf("missing membership admitted: %v", err)
	}
}

func TestPublicMonitorQueryDoesNotLoadPrivateConfiguration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := &Monitor{Name: "public label", Type: "http", Enabled: true, Target: "https://secret.example/token", Settings: []byte(`{"bearer_token":"private-token"}`), Assertions: []byte(`[{"private":"assertion"}]`), Interval: 60, Timeout: 10}
	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	sp := &StatusPage{Title: "Page", Slug: "page", Enabled: true}
	if err := s.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatusPageMonitors(ctx, sp.ID, []StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	monitors, _, err := s.ListStatusPageMonitorsWithStatus(ctx, sp.ID)
	if err != nil || len(monitors) != 1 {
		t.Fatalf("public query: %v %v", monitors, err)
	}
	got := monitors[0]
	if got.Name != m.Name || got.Type != m.Type || got.Status != "pending" || got.Target != "" || len(got.Settings) != 0 || len(got.Assertions) != 0 || got.ProxyID != nil {
		t.Fatalf("public query included private configuration: %+v", got)
	}
}

func saveStatusPageTest(s *SQLiteStore, ctx context.Context, id int64, prepare StatusPagePrepare, access MonitorAccess) (*StatusPage, error) {
	return s.SaveStatusPage(ctx, id, prepare, StatusPageAccess{Monitors: []MonitorAccess{access}, Incidents: []MonitorAccess{access}})
}

func TestStatusPageIncidentPublicationRequiresIndependentCurrentScope(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	members := []StatusPageMonitor{{MonitorID: monitors[0].ID}, {MonitorID: monitors[1].ID}}
	monitorOnly := StatusPageAccess{Monitors: []MonitorAccess{{All: true}}}
	withIncidents := statusPageEdit(&members, func(p *StatusPage) { p.ShowIncidents = true })
	if _, err := s.SaveStatusPage(ctx, 0, withIncidents, monitorOnly); !errors.Is(err, ErrStatusPageIncidentAccess) {
		t.Fatalf("monitor reads implied incident publication: %v", err)
	}
	partial := StatusPageAccess{Monitors: []MonitorAccess{{All: true}}, Incidents: []MonitorAccess{{GroupIDs: []int64{groups[0].ID}}}}
	if _, err := s.SaveStatusPage(ctx, 0, withIncidents, partial); !errors.Is(err, ErrStatusPageIncidentAccess) {
		t.Fatalf("partial incident scope admitted all members: %v", err)
	}
	incidentOnly := StatusPageAccess{Incidents: []MonitorAccess{{All: true}}}
	if _, err := s.SaveStatusPage(ctx, 0, withIncidents, incidentOnly); !errors.Is(err, ErrStatusPageMonitorAccess) {
		t.Fatalf("incident reads implied monitor publication: %v", err)
	}
	sp, err := s.SaveStatusPage(ctx, 0, statusPageEdit(&members, nil), monitorOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveStatusPage(ctx, sp.ID, statusPageEdit(nil, func(p *StatusPage) { p.ShowIncidents = true }), monitorOnly); !errors.Is(err, ErrStatusPageIncidentAccess) {
		t.Fatalf("incident enable ignored current members: %v", err)
	}
	all := StatusPageAccess{Monitors: []MonitorAccess{{All: true}}, Incidents: []MonitorAccess{{All: true}}}
	if _, err := s.SaveStatusPage(ctx, sp.ID, statusPageEdit(nil, func(p *StatusPage) { p.ShowIncidents = true }), all); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveStatusPage(ctx, sp.ID, statusPageEdit(nil, func(p *StatusPage) { p.Title = "metadata only" }), StatusPageAccess{}); err != nil {
		t.Fatalf("metadata required unrelated read: %v", err)
	}
	if _, err := s.SaveStatusPage(ctx, sp.ID, statusPageEdit(nil, func(p *StatusPage) { p.ShowIncidents = false }), StatusPageAccess{}); err != nil {
		t.Fatalf("turning incidents off required read: %v", err)
	}
}

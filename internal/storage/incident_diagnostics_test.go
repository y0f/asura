package storage

import (
	"context"
	"testing"
)

func TestIncidentCauseSearchUsesIndependentDiagnosticScope(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	for _, mon := range monitors {
		if err := s.CreateIncident(ctx, &Incident{MonitorID: mon.ID, Status: "open", Cause: "private-diagnostic-marker"}); err != nil {
			t.Fatal(err)
		}
	}
	f := IncidentListFilter{Search: "private-diagnostic-marker", Access: []MonitorAccess{{All: true}}, MonitorReadAccess: []MonitorAccess{{}}, DiagnosticsReadAccess: []MonitorAccess{{}}}
	assertCount := func(want int64) {
		t.Helper()
		result, err := s.ListIncidentsFiltered(ctx, f, Pagination{Page: 1, PerPage: 1})
		if err != nil || result.Total != want || result.TotalPages != int(want) {
			t.Fatalf("diagnostic cause inference/count: %+v, %v; want %d", result, err, want)
		}
	}
	assertCount(0)
	f.DiagnosticsReadAccess = []MonitorAccess{{MonitorIDs: []int64{monitors[0].ID}}}
	assertCount(1)
	f.Access = []MonitorAccess{{MonitorIDs: []int64{monitors[1].ID}}}
	assertCount(0)
	f.Access = []MonitorAccess{{All: true}}
	f.DiagnosticsReadAccess = []MonitorAccess{{GroupIDs: []int64{groups[0].ID}}}
	before, err := s.ListIncidentsFiltered(ctx, f, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BulkSetMonitorGroup(ctx, []int64{monitors[0].ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	assertCount(before.Total - 1)
}

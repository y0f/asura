package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestIncidentAccessFiltersCountsNamesAndCurrentMembership(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	incidents := make([]*Incident, len(monitors))
	for i, monitor := range monitors {
		incidents[i] = &Incident{MonitorID: monitor.ID, Status: "open", Cause: "connection failed"}
		if err := store.CreateIncident(ctx, incidents[i]); err != nil {
			t.Fatal(err)
		}
	}
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}, MonitorIDs: []int64{monitors[4].ID}}
	filter := IncidentListFilter{Access: []MonitorAccess{access}, MonitorReadAccess: []MonitorAccess{{MonitorIDs: []int64{monitors[2].ID}}}}
	for page, index := range []int{4, 2, 0} {
		result, err := store.ListIncidentsFiltered(ctx, filter, Pagination{Page: page + 1, PerPage: 1})
		if err != nil {
			t.Fatal(err)
		}
		rows := result.Data.([]*Incident)
		if result.Total != 3 || result.TotalPages != 3 || len(rows) != 1 || rows[0].ID != incidents[index].ID {
			t.Fatalf("page %d/count leaked inaccessible incidents: %+v", page+1, result)
		}
		wantName := ""
		if index == 2 {
			wantName = monitors[index].Name
		}
		if rows[0].MonitorName != wantName || !sameGroupID(rows[0].MonitorGroupID, monitors[index].GroupID) {
			t.Fatalf("independent name scope or current ownership lost: %+v", rows[0])
		}
	}
	for _, search := range []string{monitors[0].Name, monitors[2].Name, "connection"} {
		filter.Search = search
		result, err := store.ListIncidentsFiltered(ctx, filter, Pagination{Page: 1, PerPage: 10})
		want := int64(0)
		if search == monitors[2].Name {
			want = 1
		} else if search == "connection" {
			want = 3
		}
		if err != nil || result.Total != want {
			t.Fatalf("search %q inferred hidden names or lost permitted matches: %+v, %v", search, result, err)
		}
	}
	filter.Search = ""
	filter.Access = append(filter.Access, MonitorAccess{MonitorIDs: []int64{monitors[2].ID, monitors[3].ID}})
	result, err := store.ListIncidentsFiltered(ctx, filter, Pagination{Page: 1, PerPage: 10})
	if err != nil || result.Total != 1 || result.Data.([]*Incident)[0].ID != incidents[2].ID {
		t.Fatalf("action scopes were unioned: %+v, %v", result, err)
	}
	if _, err := store.GetIncident(ctx, incidents[1].ID, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hidden incident detail returned %v", err)
	}
	if err := store.DeleteIncident(ctx, incidents[1].ID, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hidden incident delete returned %v", err)
	}
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{monitors[0].ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetIncident(ctx, incidents[0].ID, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old group membership retained incident access: %v", err)
	}
	if err := store.DeleteIncident(ctx, incidents[4].ID, access); err != nil {
		t.Fatalf("explicit owner grant cannot delete incident: %v", err)
	}
	result, err = store.ListIncidentsFiltered(ctx, IncidentListFilter{Access: []MonitorAccess{{}}}, Pagination{Page: 1, PerPage: 10})
	if err != nil || result.Total != 0 || len(result.Data.([]*Incident)) != 0 {
		t.Fatalf("empty filter failed open: %+v, %v", result, err)
	}
}

func sameGroupID(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

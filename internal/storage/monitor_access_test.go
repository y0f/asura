package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func accessFixture(t *testing.T) (*SQLiteStore, []*MonitorGroup, []*Monitor) {
	t.Helper()
	store := testStore(t)
	ctx := context.Background()
	groups := []*MonitorGroup{{Name: "production"}, {Name: "private"}}
	for _, group := range groups {
		if err := store.CreateMonitorGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
	}
	var monitors []*Monitor
	for i := range 6 {
		monitor := &Monitor{Name: fmt.Sprintf("monitor-%d", i), Type: "http", Target: "https://example.test", Enabled: true, Interval: 60, Timeout: 10}
		if i < 4 {
			monitor.GroupID = &groups[i%2].ID
		}
		if err := store.CreateMonitor(ctx, monitor); err != nil {
			t.Fatal(err)
		}
		monitors = append(monitors, monitor)
	}
	return store, groups, monitors
}

func TestMonitorAccessFiltersBeforePaginationAndCount(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	access := MonitorAccess{MonitorIDs: []int64{monitors[4].ID}, GroupIDs: []int64{groups[0].ID}}
	want := []int64{monitors[0].ID, monitors[2].ID, monitors[4].ID}
	for page, id := range want {
		result, err := store.ListMonitors(ctx, MonitorListFilter{Access: []MonitorAccess{access}}, Pagination{Page: page + 1, PerPage: 1})
		if err != nil {
			t.Fatal(err)
		}
		rows := result.Data.([]*Monitor)
		if result.Total != 3 || result.TotalPages != 3 || len(rows) != 1 || rows[0].ID != id {
			t.Fatalf("page %d exposed hidden records or incorrect totals: %+v, rows=%+v", page+1, result, rows)
		}
	}
	result, err := store.ListMonitors(ctx, MonitorListFilter{Access: []MonitorAccess{access, {MonitorIDs: []int64{monitors[2].ID, monitors[3].ID}}}}, Pagination{Page: 1, PerPage: 10})
	if err != nil || result.Total != 1 || result.Data.([]*Monitor)[0].ID != monitors[2].ID {
		t.Fatalf("action intersection became a union: %+v, %v", result, err)
	}
	for _, hidden := range []*Monitor{monitors[1], monitors[3], monitors[5]} {
		if _, err := store.GetMonitor(ctx, hidden.ID, access); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("hidden ID %d returned %v", hidden.ID, err)
		}
	}
	result, err = store.ListMonitors(ctx, MonitorListFilter{Access: []MonitorAccess{{}}}, Pagination{Page: 1, PerPage: 10})
	if err != nil || result.Total != 0 || len(result.Data.([]*Monitor)) != 0 {
		t.Fatalf("zero filter did not deny: %+v, %v", result, err)
	}
	if _, err := store.GetMonitor(ctx, monitors[0].ID, MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("zero object filter did not deny: %v", err)
	}
}

func TestMonitorAccessMutationsCheckCurrentOwnerAndDestination(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	monitor, err := store.GetMonitor(ctx, monitors[0].ID, access)
	if err != nil {
		t.Fatal(err)
	}
	monitor.GroupID = &groups[1].ID
	if err := store.UpdateMonitor(ctx, monitor, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("move to inaccessible destination returned %v", err)
	}
	monitor.GroupID = nil
	if err := store.UpdateMonitor(ctx, monitor, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("move to ungrouped destination returned %v", err)
	}
	monitor.GroupID = &groups[0].ID
	monitor.Name = "authorized name"
	if err := store.UpdateMonitor(ctx, monitor, access); err != nil {
		t.Fatal(err)
	}
	// Another authorized writer moves the monitor after this editor loaded it.
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{monitor.ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	monitor.Name = "stale editor overwrite"
	if err := store.UpdateMonitor(ctx, monitor, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale source-group authority returned %v", err)
	}
	if err := store.SetMonitorEnabled(ctx, monitor.ID, false, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pause followed stale group ownership: %v", err)
	}
	if err := store.DeleteMonitor(ctx, monitor.ID, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete followed stale group ownership: %v", err)
	}
	stored, err := store.GetMonitor(ctx, monitor.ID)
	if err != nil || stored.Name != "authorized name" || !stored.Enabled || stored.GroupID == nil || *stored.GroupID != groups[1].ID {
		t.Fatalf("denied mutations changed stored monitor: %+v, %v", stored, err)
	}
	// An explicit ID binding follows that monitor across group membership changes.
	if err := store.SetMonitorEnabled(ctx, monitor.ID, false, MonitorAccess{MonitorIDs: []int64{monitor.ID}}); err != nil {
		t.Fatal(err)
	}
}

func TestMonitorIDGrantCannotMoveIntoUnassignedGroup(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	monitor := monitors[0]
	idScope := MonitorAccess{MonitorIDs: []int64{monitor.ID}}
	monitor.Name = "ID-authorized ordinary edit"
	if err := store.UpdateMonitor(ctx, monitor, idScope); err != nil {
		t.Fatalf("ID grant rejected unchanged group edit: %v", err)
	}
	monitor.GroupID = &groups[1].ID
	if err := store.UpdateMonitor(ctx, monitor, idScope); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ID-only grant moved into another group: %v", err)
	}
	for _, destination := range []*int64{&groups[1].ID, nil} {
		if n, err := store.BulkSetMonitorGroup(ctx, []int64{monitor.ID}, destination, idScope); n != 0 || !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("ID-only bulk grant moved into another group: %d, %v", n, err)
		}
	}
	if n, err := store.BulkSetMonitorGroup(ctx, []int64{monitor.ID}, &groups[0].ID, idScope); n != 1 || err != nil {
		t.Fatalf("ID grant rejected unchanged group bulk edit: %d, %v", n, err)
	}
	if err := store.UpdateMonitor(ctx, monitor, MonitorAccess{MonitorIDs: []int64{monitor.ID}, GroupIDs: []int64{groups[1].ID}}); err != nil {
		t.Fatalf("source ID plus explicit destination group rejected: %v", err)
	}
}

func TestMonitorCreationRequiresDestinationScope(t *testing.T) {
	store, groups, _ := accessFixture(t)
	ctx := context.Background()
	monitor := &Monitor{Name: "new", Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10}
	groupScope := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	for _, access := range []MonitorAccess{{}, {MonitorIDs: []int64{7}}, groupScope} {
		if err := store.CreateMonitor(ctx, monitor, access); !errors.Is(err, sql.ErrNoRows) || monitor.ID != 0 {
			t.Fatalf("ungrouped creation accepted with %+v: %v", access, err)
		}
	}
	monitor.GroupID = &groups[1].ID
	if err := store.CreateMonitor(ctx, monitor, groupScope); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("creation in inaccessible group returned %v", err)
	}
	monitor.GroupID = &groups[0].ID
	if err := store.CreateMonitor(ctx, monitor, groupScope); err != nil || monitor.ID == 0 {
		t.Fatalf("authorized destination creation failed: %v", err)
	}
}

func TestBulkMonitorAccessRejectsTheWholeSet(t *testing.T) {
	for _, operation := range []string{"pause", "delete", "move"} {
		t.Run(operation, func(t *testing.T) {
			store, groups, monitors := accessFixture(t)
			ctx := context.Background()
			access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
			mutate := func(ids []int64, filters ...MonitorAccess) (int64, error) {
				switch operation {
				case "pause":
					return store.BulkSetMonitorsEnabled(ctx, ids, false, filters...)
				case "delete":
					return store.BulkDeleteMonitors(ctx, ids, filters...)
				default:
					return store.BulkSetMonitorGroup(ctx, ids, &groups[0].ID, filters...)
				}
			}
			for _, ids := range [][]int64{
				{monitors[0].ID, monitors[1].ID},
				{monitors[0].ID, 9999},
				{monitors[0].ID, -1},
			} {
				if n, err := mutate(ids, access); n != 0 || !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("partial or inaccessible set changed %d records: %v", n, err)
				}
			}
			// Each action must cover every ID, even if their union covers the set.
			if n, err := mutate([]int64{monitors[0].ID, monitors[2].ID},
				MonitorAccess{MonitorIDs: []int64{monitors[0].ID}},
				MonitorAccess{MonitorIDs: []int64{monitors[2].ID}}); n != 0 || !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("disjoint action scopes changed %d records: %v", n, err)
			}
			for _, original := range monitors {
				stored, err := store.GetMonitor(ctx, original.ID)
				if err != nil || !stored.Enabled || !sameOptionalID(stored.GroupID, original.GroupID) {
					t.Fatalf("denied bulk changed monitor %d: %+v, %v", original.ID, stored, err)
				}
			}
			if n, err := mutate([]int64{monitors[0].ID, monitors[2].ID, monitors[0].ID}, access); n != 2 || err != nil {
				t.Fatalf("authorized duplicate IDs changed %d records: %v", n, err)
			}
		})
	}
}

func sameOptionalID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func TestMonitorNameSearchDoesNotProbeHiddenTargetSecrets(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	monitors[0].Target = "https://user:hidden-password@example.test/?token=hidden-query"
	if err := store.UpdateMonitor(ctx, monitors[0]); err != nil {
		t.Fatal(err)
	}
	filter := MonitorListFilter{Access: []MonitorAccess{{GroupIDs: []int64{groups[0].ID}}}}
	for _, prefix := range []string{"https://user:hidden", "https://user:wrong", "https://user:hidden-password@example.test/?token=hidden", "https://user:hidden-password@example.test/?token=wrong"} {
		filter.Search = prefix
		result, err := store.ListMonitors(ctx, filter, Pagination{Page: 1, PerPage: 1})
		if err != nil || result.Total != 0 || len(result.Data.([]*Monitor)) != 0 {
			t.Fatalf("target probe affected rows/count: %+v %v", result, err)
		}
	}
	filter.Search = "monitor-"
	result, err := store.ListMonitors(ctx, filter, Pagination{Page: 1, PerPage: 1})
	if err != nil || result.Total != 2 || result.Data.([]*Monitor)[0].ID != monitors[0].ID {
		t.Fatalf("name search lost current group scope: %+v %v", result, err)
	}
}

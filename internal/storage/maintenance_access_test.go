package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceAccessRequiresEveryCurrentTarget(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	makeWindow := func(name string, ids ...int64) *MaintenanceWindow {
		return &MaintenanceWindow{Name: name, MonitorIDs: ids, Recurring: "manual", StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)}
	}
	allowed := makeWindow("allowed", monitors[0].ID, monitors[2].ID)
	if err := store.CreateMaintenanceWindow(ctx, allowed, access); err != nil {
		t.Fatal(err)
	}
	for _, window := range []*MaintenanceWindow{
		makeWindow("mixed targets", monitors[0].ID, monitors[1].ID),
		makeWindow("all targets"),
		makeWindow("missing target", monitors[0].ID, 9999),
	} {
		if err := store.CreateMaintenanceWindow(ctx, window, access); !errors.Is(err, sql.ErrNoRows) || window.ID != 0 {
			t.Fatalf("incomplete target authority created %+v: %v", window, err)
		}
	}
	all := makeWindow("all")
	mixed := makeWindow("mixed", monitors[0].ID, monitors[1].ID)
	ungrouped := makeWindow("ungrouped", monitors[5].ID)
	for _, window := range []*MaintenanceWindow{all, mixed, ungrouped} {
		if err := store.CreateMaintenanceWindow(ctx, window, MonitorAccess{All: true}); err != nil {
			t.Fatal(err)
		}
	}
	visible, err := store.ListMaintenanceWindows(ctx, access)
	if err != nil || len(visible) != 1 || visible[0].ID != allowed.ID {
		t.Fatalf("window collection accepted partial coverage: %+v, %v", visible, err)
	}
	for _, window := range []*MaintenanceWindow{all, mixed, ungrouped} {
		if _, err := store.GetMaintenanceWindow(ctx, window.ID, access); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("inaccessible window read: %v", err)
		}
		if err := store.ToggleMaintenanceWindow(ctx, window.ID, true, access); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("inaccessible window toggled: %v", err)
		}
		if err := store.DeleteMaintenanceWindow(ctx, window.ID, access); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("inaccessible window deleted: %v", err)
		}
	}
	// SQL NULL group membership must not make NOT EXISTS treat an inaccessible
	// target as authorized, including an OR binding with unrelated explicit IDs.
	mixedScope := MonitorAccess{MonitorIDs: []int64{monitors[4].ID}, GroupIDs: []int64{groups[0].ID}}
	for _, filter := range []MonitorAccess{access, mixedScope} {
		visible, err := store.ListMaintenanceWindows(ctx, filter)
		if err != nil || len(visible) != 1 || visible[0].ID != allowed.ID {
			t.Fatalf("NULL group membership widened list scope: %+v, %v", visible, err)
		}
		if _, err := store.GetMaintenanceWindow(ctx, ungrouped.ID, filter); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("NULL group membership widened detail scope: %v", err)
		}
		replacement := *ungrouped
		replacement.MonitorIDs = []int64{monitors[0].ID}
		if err := store.UpdateMaintenanceWindow(ctx, &replacement, filter); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("NULL group membership widened update scope: %v", err)
		}
		if err := store.ToggleMaintenanceWindow(ctx, ungrouped.ID, true, filter); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("NULL group membership widened toggle scope: %v", err)
		}
		if err := store.DeleteMaintenanceWindow(ctx, ungrouped.ID, filter); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("NULL group membership widened delete scope: %v", err)
		}
	}
	// Covering all currently existing IDs still cannot authorize a future-wide window.
	var ids []int64
	for _, monitor := range monitors {
		ids = append(ids, monitor.ID)
	}
	if _, err := store.GetMaintenanceWindow(ctx, all.ID, MonitorAccess{MonitorIDs: ids}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ID coverage became global maintenance authority: %v", err)
	}
	if err := store.ToggleMaintenanceWindow(ctx, allowed.ID, true, access); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{monitors[0].ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ToggleMaintenanceWindow(ctx, allowed.ID, false, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale group authority toggled maintenance: %v", err)
	}
	stored, err := store.GetMaintenanceWindow(ctx, allowed.ID)
	if err != nil || !stored.Active {
		t.Fatalf("denied toggle changed state: %+v, %v", stored, err)
	}
}

func TestMaintenanceUpdateChecksExistingAndReplacementTargets(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	window := &MaintenanceWindow{Name: "original", Recurring: "manual", MonitorIDs: []int64{monitors[0].ID}}
	if err := store.CreateMaintenanceWindow(ctx, window, access); err != nil {
		t.Fatal(err)
	}
	window.Name = "denied replacement"
	window.MonitorIDs = []int64{monitors[1].ID}
	if err := store.UpdateMaintenanceWindow(ctx, window, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replacement outside scope accepted: %v", err)
	}
	window.MonitorIDs = nil
	if err := store.UpdateMaintenanceWindow(ctx, window, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replacement broadened to all monitors: %v", err)
	}
	stored, err := store.GetMaintenanceWindow(ctx, window.ID)
	if err != nil || stored.Name != "original" || len(stored.MonitorIDs) != 1 || stored.MonitorIDs[0] != monitors[0].ID {
		t.Fatalf("denied replacement changed stored data: %+v, %v", stored, err)
	}
	window.MonitorIDs = []int64{monitors[2].ID}
	window.Name = "authorized replacement"
	if err := store.UpdateMaintenanceWindow(ctx, window, access); err != nil {
		t.Fatal(err)
	}
	// The new target is allowed, but the stored window was moved out of scope.
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{monitors[2].ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	window.MonitorIDs = []int64{monitors[0].ID}
	if err := store.UpdateMaintenanceWindow(ctx, window, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replacement skipped authorization of current targets: %v", err)
	}
}

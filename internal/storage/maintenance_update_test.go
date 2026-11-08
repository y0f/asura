package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceUpdateSeparatesCurrentAndDestinationToggleAuthority(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	all := []MonitorAccess{{All: true}}
	groupA := []MonitorAccess{{GroupIDs: []int64{groups[0].ID}}}
	deny := []MonitorAccess{{}}
	window := &MaintenanceWindow{Name: "original", Recurring: "manual", MonitorIDs: []int64{monitors[0].ID}}
	if err := store.CreateMaintenanceWindow(ctx, window); err != nil {
		t.Fatal(err)
	}
	metadata := func(current *MaintenanceWindow) (*MaintenanceWindow, error) {
		current.Name = "renamed"
		return current, nil
	}
	got, err := store.UpdateMaintenanceWindowWithActions(ctx, window.ID, metadata, MaintenanceUpdateAccess{Update: groupA, Toggle: deny})
	if err != nil || got.Name != "renamed" || got.Active {
		t.Fatalf("metadata update needs no toggle grant: %+v %v", got, err)
	}
	for _, tc := range []struct {
		name   string
		access MaintenanceUpdateAccess
		move   bool
	}{
		{"zero access", MaintenanceUpdateAccess{}, false},
		{"missing toggle access", MaintenanceUpdateAccess{Update: all}, false},
		{"update without toggle", MaintenanceUpdateAccess{Update: all, Toggle: deny}, false},
		{"toggle without update", MaintenanceUpdateAccess{Update: deny, Toggle: all}, false},
		{"destination outside toggle", MaintenanceUpdateAccess{Update: all, Toggle: groupA}, true},
		{"source outside toggle", MaintenanceUpdateAccess{Update: all, Toggle: []MonitorAccess{{GroupIDs: []int64{groups[1].ID}}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			got, err := store.UpdateMaintenanceWindowWithActions(ctx, window.ID, func(current *MaintenanceWindow) (*MaintenanceWindow, error) {
				called = true
				current.Name = "forbidden"
				current.Active = true
				if tc.move {
					current.MonitorIDs = []int64{monitors[1].ID}
				}
				return current, nil
			}, tc.access)
			if !errors.Is(err, sql.ErrNoRows) || got != nil {
				t.Fatalf("denied action returned %+v %v", got, err)
			}
			if tc.name == "toggle without update" && called {
				t.Fatal("callback saw row without update authority")
			}
			stored, err := store.GetMaintenanceWindow(ctx, window.ID)
			if err != nil || stored.Name != "renamed" || stored.Active || stored.MonitorIDs[0] != monitors[0].ID {
				t.Fatalf("denied edit changed data: %+v %v", stored, err)
			}
		})
	}
	got, err = store.UpdateMaintenanceWindowWithActions(ctx, window.ID, func(current *MaintenanceWindow) (*MaintenanceWindow, error) {
		current.Active = true
		current.MonitorIDs = []int64{monitors[1].ID}
		return current, nil
	}, MaintenanceUpdateAccess{Update: all, Toggle: all})
	if err != nil || !got.Active || got.MonitorIDs[0] != monitors[1].ID {
		t.Fatalf("fully authorized move/toggle failed: %+v %v", got, err)
	}
}

func TestMaintenanceUpdatePreservesConcurrentStateAndRollsBackPrepareFailure(t *testing.T) {
	store, _, monitors := accessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := &MaintenanceWindow{Name: "original", Recurring: "manual", MonitorIDs: []int64{monitors[0].ID}}
	if err := store.CreateMaintenanceWindow(ctx, window); err != nil {
		t.Fatal(err)
	}
	// Hold a newer toggle in the single writer connection while the metadata edit
	// waits. The callback must receive that committed state rather than an HTTP
	// handler's earlier snapshot.
	tx, err := store.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE maintenance_windows SET active=1 WHERE id=?", window.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.UpdateMaintenanceWindowWithActions(ctx, window.ID, func(current *MaintenanceWindow) (*MaintenanceWindow, error) {
			current.Name = "after toggle"
			return current, nil
		}, MaintenanceUpdateAccess{Update: []MonitorAccess{{All: true}}, Toggle: []MonitorAccess{{}}})
		done <- err
	}()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stored, err := store.GetMaintenanceWindow(ctx, window.ID)
	if err != nil || !stored.Active || stored.Name != "after toggle" {
		t.Fatalf("omitted active reverted newer toggle: %+v %v", stored, err)
	}
	injected := errors.New("prepare rejected")
	got, err := store.UpdateMaintenanceWindowWithActions(ctx, window.ID, func(current *MaintenanceWindow) (*MaintenanceWindow, error) {
		current.Name = "not saved"
		current.MonitorIDs[0] = monitors[1].ID
		return current, injected
	}, MaintenanceUpdateAccess{Update: []MonitorAccess{{All: true}}, Toggle: []MonitorAccess{{All: true}}})
	if !errors.Is(err, injected) || got != nil {
		t.Fatalf("prepare error lost: %+v %v", got, err)
	}
	stored, err = store.GetMaintenanceWindow(ctx, window.ID)
	if err != nil || stored.Name != "after toggle" || stored.MonitorIDs[0] != monitors[0].ID {
		t.Fatalf("prepare error changed data: %+v %v", stored, err)
	}
}

func TestMaintenanceConcurrentFlipsSerializeWithoutLosingTransitions(t *testing.T) {
	store, _, monitors := accessFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := &MaintenanceWindow{Name: "current", MonitorIDs: []int64{monitors[0].ID}, Recurring: "manual"}
	if err := store.CreateMaintenanceWindow(ctx, window); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FlipMaintenanceWindow(ctx, window.ID, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty flip access did not deny: %v", err)
	}
	type result struct {
		window *MaintenanceWindow
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			got, err := store.FlipMaintenanceWindow(ctx, window.ID, []MonitorAccess{{All: true}})
			results <- result{got, err}
		}()
	}
	close(start)
	states := make(map[bool]int)
	for range 2 {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatal(got.err)
			}
			states[got.window.Active]++
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	stored, err := store.GetMaintenanceWindow(ctx, window.ID)
	if err != nil || stored.Active || states[true] != 1 || states[false] != 1 {
		t.Fatalf("two flips lost a transition: %+v %v states%v", stored, err, states)
	}
}

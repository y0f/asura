package storage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
)

func TestDeleteEmptyGroupNeverMovesMonitors(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	if err := s.DeleteEmptyMonitorGroup(ctx, groups[0].ID); !errors.Is(err, ErrGroupNotEmpty) {
		t.Fatalf("populated group deletion: %v", err)
	}
	m, err := s.GetMonitor(ctx, monitors[0].ID)
	if err != nil || m.GroupID == nil || *m.GroupID != groups[0].ID {
		t.Fatalf("rejected deletion moved monitor: %+v, %v", m, err)
	}
	empty := &MonitorGroup{Name: "empty"}
	if err := s.CreateMonitorGroup(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEmptyMonitorGroup(ctx, empty.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEmptyMonitorGroup(ctx, empty.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing group deletion: %v", err)
	}
}

func TestDeleteEmptyGroupSerializesWithAssignment(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	g := &MonitorGroup{Name: "race"}
	if err := s.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	m := relationErrorMonitor()
	m.GroupID = &g.ID
	start := make(chan struct{})
	var createErr, deleteErr error
	var wg sync.WaitGroup
	wg.Go(func() { <-start; createErr = s.CreateMonitorWithRelations(ctx, m, MonitorCreateOptions{}) })
	wg.Go(func() { <-start; deleteErr = s.DeleteEmptyMonitorGroup(ctx, g.ID) })
	close(start)
	wg.Wait()
	switch {
	case createErr == nil:
		if !errors.Is(deleteErr, ErrGroupNotEmpty) {
			t.Fatalf("assignment committed but deletion did not reject: %v", deleteErr)
		}
	case deleteErr == nil:
		if !errors.Is(createErr, ErrInvalidMonitorRelation) {
			t.Fatalf("deletion committed but assignment did not reject: %v", createErr)
		}
	default:
		t.Fatalf("neither operation committed: create=%v delete=%v", createErr, deleteErr)
	}
}

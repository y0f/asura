package incident

import (
	"context"
	"os"
	"testing"

	"github.com/y0f/asura/internal/storage"
	"log/slog"
)

func testStore(t *testing.T) storage.Store {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "asura-incident-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	store, err := storage.NewSQLiteStore(tmpFile.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestIncidentLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a monitor
	m := &storage.Monitor{
		Name:             "Test",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		Tags:             []string{},
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	store.CreateMonitor(ctx, m)

	mgr := NewManager(store, logger)

	// First failure should create incident
	inc, created, err := mgr.ProcessFailure(ctx, m.ID, m.Name, "down", "connection timeout")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected incident to be created")
	}
	if inc.Status != StatusOpen {
		t.Fatalf("expected open, got %s", inc.Status)
	}

	// Second failure should add event, not create new incident
	inc2, created2, err := mgr.ProcessFailure(ctx, m.ID, m.Name, "down", "connection timeout again")
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("expected no new incident")
	}
	if inc2.ID != inc.ID {
		t.Fatal("expected same incident")
	}

	// Recovery should resolve
	resolved, wasResolved, err := mgr.ProcessRecovery(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !wasResolved {
		t.Fatal("expected incident to be resolved")
	}
	if resolved.Status != StatusResolved {
		t.Fatalf("expected resolved, got %s", resolved.Status)
	}

	// Another recovery should be no-op
	_, wasResolved2, err := mgr.ProcessRecovery(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wasResolved2 {
		t.Fatal("expected no resolution")
	}
}

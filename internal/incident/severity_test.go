package incident

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

// A degraded->down transition on an open incident must upgrade severity
// (or otherwise signal the escalation); today it stays "warning".
func TestSeverityRaisedDegradedToDown(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	m := &storage.Monitor{
		Name: "Test", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true, Tags: []string{},
		FailureThreshold: 1, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(store, logger)

	inc, created, err := mgr.ProcessFailure(ctx, m.ID, m.Name, "degraded", "slow")
	if err != nil || !created {
		t.Fatalf("setup: created=%v err=%v", created, err)
	}
	if inc.Severity != SeverityWarning {
		t.Fatalf("setup: severity=%q", inc.Severity)
	}

	_, created2, err := mgr.ProcessFailure(ctx, m.ID, m.Name, "down", "connection refused")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Severity != SeverityCritical {
		t.Errorf("monitor went degraded->down but incident severity is %q (created=%v); want %q",
			stored.Severity, created2, SeverityCritical)
	}
}

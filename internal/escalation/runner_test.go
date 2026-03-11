package escalation

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
)

func testStore(t *testing.T) storage.Store {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "asura-esc-test-*.db")
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestStartEscalation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()

	ep := &storage.EscalationPolicy{Name: "Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	steps := []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{1}},
		{StepOrder: 1, DelayMinutes: 5, NotificationChannelIDs: []int64{2}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, steps); err != nil {
		t.Fatal(err)
	}

	mon := &storage.Monitor{
		Name: "Test Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
		EscalationPolicyID: &ep.ID,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &storage.Incident{MonitorID: mon.ID, Status: "open", Cause: "timeout"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	StartEscalation(ctx, store, mon, inc.ID, logger)

	state, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.PolicyID != ep.ID {
		t.Fatalf("expected policy %d, got %d", ep.ID, state.PolicyID)
	}
	if state.CurrentStep != 0 {
		t.Fatalf("expected step 0, got %d", state.CurrentStep)
	}
}

func TestStartEscalationNoPolicy(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()

	mon := &storage.Monitor{
		Name: "No Policy", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &storage.Incident{MonitorID: mon.ID, Status: "open", Cause: "timeout"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	StartEscalation(ctx, store, mon, inc.ID, logger)

	_, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err == nil {
		t.Fatal("expected error (no state created), got nil")
	}
}

func TestCancelEscalation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()

	ep := &storage.EscalationPolicy{Name: "Cancel Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	steps := []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{1}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, steps); err != nil {
		t.Fatal(err)
	}

	mon := &storage.Monitor{
		Name: "Cancel Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
		EscalationPolicyID: &ep.ID,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &storage.Incident{MonitorID: mon.ID, Status: "open", Cause: "timeout"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	StartEscalation(ctx, store, mon, inc.ID, logger)

	state, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("expected state to exist")
	}

	CancelEscalation(ctx, store, inc.ID)

	_, err = store.GetEscalationStateByIncident(ctx, inc.ID)
	if err == nil {
		t.Fatal("expected error after cancel, got nil")
	}
}

func TestTickProcessesState(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()

	// Create notification channel so dispatcher has something to look up
	ch := &storage.NotificationChannel{
		Name: "test-ch", Type: "webhook", Enabled: true,
		Settings: []byte(`{"url":"http://localhost"}`),
	}
	if err := store.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}

	ep := &storage.EscalationPolicy{Name: "Tick Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	steps := []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{ch.ID}},
		{StepOrder: 1, DelayMinutes: 5, NotificationChannelIDs: []int64{ch.ID}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, steps); err != nil {
		t.Fatal(err)
	}

	mon := &storage.Monitor{
		Name: "Tick Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
		EscalationPolicyID: &ep.ID,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &storage.Incident{MonitorID: mon.ID, Status: "open", Cause: "timeout"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	state := &storage.EscalationState{
		IncidentID:  inc.ID,
		PolicyID:    ep.ID,
		CurrentStep: 0,
		NextFireAt:  time.Now().Add(-time.Minute),
	}
	if err := store.CreateEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}

	dispatcher := notifier.NewDispatcher(store, logger, false)
	runner := NewRunner(store, dispatcher, logger)

	runner.Tick(ctx)

	updated, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CurrentStep != 1 {
		t.Fatalf("expected step to advance to 1, got %d", updated.CurrentStep)
	}
}

func TestTickDeletesStateOnResolvedIncident(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()

	ep := &storage.EscalationPolicy{Name: "Resolved Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	steps := []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{1}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, steps); err != nil {
		t.Fatal(err)
	}

	mon := &storage.Monitor{
		Name: "Resolved Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &storage.Incident{MonitorID: mon.ID, Status: "resolved", Cause: "timeout"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	state := &storage.EscalationState{
		IncidentID:  inc.ID,
		PolicyID:    ep.ID,
		CurrentStep: 0,
		NextFireAt:  time.Now().Add(-time.Minute),
	}
	if err := store.CreateEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}

	dispatcher := notifier.NewDispatcher(store, logger, false)
	runner := NewRunner(store, dispatcher, logger)

	runner.Tick(ctx)

	_, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err == nil {
		t.Fatal("expected state to be deleted for resolved incident")
	}
}

func TestTickDeletesStateOnDisabledPolicy(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()

	ep := &storage.EscalationPolicy{Name: "Disabled Test", Enabled: false}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	steps := []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{1}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, steps); err != nil {
		t.Fatal(err)
	}

	mon := &storage.Monitor{
		Name: "Disabled Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &storage.Incident{MonitorID: mon.ID, Status: "open", Cause: "timeout"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	state := &storage.EscalationState{
		IncidentID:  inc.ID,
		PolicyID:    ep.ID,
		CurrentStep: 0,
		NextFireAt:  time.Now().Add(-time.Minute),
	}
	if err := store.CreateEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}

	dispatcher := notifier.NewDispatcher(store, logger, false)
	runner := NewRunner(store, dispatcher, logger)

	runner.Tick(ctx)

	_, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err == nil {
		t.Fatal("expected state to be deleted for disabled policy")
	}
}

func TestHelpers(t *testing.T) {
	t.Run("MarshalUnmarshalChannelIDs", func(t *testing.T) {
		ids := []int64{1, 5, 10}
		s := MarshalChannelIDs(ids)
		got := UnmarshalChannelIDs(s)
		if len(got) != 3 || got[0] != 1 || got[1] != 5 || got[2] != 10 {
			t.Fatalf("roundtrip failed: %v", got)
		}
	})

	t.Run("FormatStepSummary", func(t *testing.T) {
		if s := FormatStepSummary(nil); s != "no steps" {
			t.Fatalf("expected 'no steps', got %q", s)
		}
		steps := []*storage.EscalationPolicyStep{
			{DelayMinutes: 0},
		}
		if s := FormatStepSummary(steps); s != "1 step at 0m" {
			t.Fatalf("expected '1 step at 0m', got %q", s)
		}
		steps = append(steps, &storage.EscalationPolicyStep{DelayMinutes: 5})
		if s := FormatStepSummary(steps); s != "2 steps, first at 0m" {
			t.Fatalf("expected '2 steps, first at 0m', got %q", s)
		}
	})
}

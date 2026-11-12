package escalation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/notifier"
	"github.com/y0f/asura/v3/internal/storage"
)

func escalationFixture(t *testing.T, store storage.Store, delay int, repeat bool) (*storage.Monitor, *storage.Incident, *int64) {
	t.Helper()
	ctx := context.Background()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	ch := &storage.NotificationChannel{
		Name: "hook", Type: "webhook", Enabled: true,
		Settings: []byte(`{"url":"` + srv.URL + `"}`),
	}
	if err := store.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	ep := &storage.EscalationPolicy{Name: "p", Enabled: true, Repeat: repeat}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: delay, NotificationChannelIDs: []int64{ch.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	mon := &storage.Monitor{
		Name: "m", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 1, SuccessThreshold: 1, EscalationPolicyID: &ep.ID,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	inc := &storage.Incident{MonitorID: mon.ID, Status: "open", Cause: "down"}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEscalationState(ctx, &storage.EscalationState{
		IncidentID: inc.ID, PolicyID: ep.ID, CurrentStep: 0,
		NextFireAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	return mon, inc, &hits
}

func waitHits(hits *int64, want int64) int64 {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(hits) < want {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	return atomic.LoadInt64(hits)
}

// Escalation must not page while the monitor is in a maintenance window.
func TestEscalationHeldDuringMaintenance(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()
	mon, _, hits := escalationFixture(t, store, 0, false)

	now := time.Now()
	mw := &storage.MaintenanceWindow{
		Name: "mw", MonitorIDs: []int64{mon.ID},
		StartTime: now.Add(-time.Hour), EndTime: now.Add(time.Hour),
	}
	if err := store.CreateMaintenanceWindow(ctx, mw); err != nil {
		t.Fatal(err)
	}
	if in, _ := store.IsMonitorInMaintenance(ctx, mon.ID, time.Now()); !in {
		t.Fatal("setup: monitor should be in maintenance")
	}

	dispatcher := notifier.NewDispatcher(store, logger, true)
	t.Cleanup(func() { dispatcher.Shutdown(context.Background()) })
	r := NewRunner(store, dispatcher, logger)
	r.Tick(ctx)
	if got := waitHits(hits, 1); got != 0 {
		t.Errorf("escalation step dispatched %d notification(s) during maintenance", got)
	}
}

// A repeating single-step policy with delay 0 re-pages on every tick with no pause.
func TestEscalationRepeatPausesBetweenCycles(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()
	_, inc, hits := escalationFixture(t, store, 0, true)

	dispatcher := notifier.NewDispatcher(store, logger, true)
	t.Cleanup(func() { dispatcher.Shutdown(context.Background()) })
	r := NewRunner(store, dispatcher, logger)
	for i := 0; i < 5; i++ {
		r.Tick(ctx) // each real tick is 30s apart; NextFireAt = now+0 is always due
	}
	st, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := waitHits(hits, 5)
	if got > 1 {
		t.Errorf("repeat policy with step-0 delay 0 paged %d times in 5 consecutive ticks (next_fire_at=%s, i.e. no pause)",
			got, st.NextFireAt.Sub(time.Now()).Round(time.Second))
	}
}

func TestEscalationRetainsStepWhenNotificationAdmissionFails(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := testLogger()
	_, inc, _ := escalationFixture(t, store, 0, false)
	dispatcher := notifier.NewDispatcher(store, logger, true)
	dispatcher.Shutdown(ctx)
	NewRunner(store, dispatcher, logger).Tick(ctx)
	state, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil || state.CurrentStep != 0 || state.NextFireAt.After(time.Now()) {
		t.Fatalf("failed admission consumed escalation step: %+v %v", state, err)
	}
}

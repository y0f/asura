package monitor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

func testStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "asura-monitor-test-*.db")
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHashBody(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		h1 := HashBody("hello world")
		h2 := HashBody("hello world")
		if h1 != h2 {
			t.Fatal("expected same hash for same input")
		}
		if len(h1) != 64 {
			t.Fatalf("expected 64 hex chars, got %d", len(h1))
		}
	})

	t.Run("different inputs differ", func(t *testing.T) {
		h1 := HashBody("hello")
		h2 := HashBody("world")
		if h1 == h2 {
			t.Fatal("different inputs should have different hashes")
		}
	})
}

func TestEvaluateAssertions(t *testing.T) {
	t.Run("no assertions returns original status", func(t *testing.T) {
		mon := &storage.Monitor{}
		result := &checker.Result{Status: "up"}
		got := evaluateAssertions(mon, result)
		if got != "up" {
			t.Fatalf("expected up, got %s", got)
		}
	})

	t.Run("empty conditions returns original", func(t *testing.T) {
		mon := &storage.Monitor{Assertions: json.RawMessage(`{"operator":"and","groups":[]}`)}
		result := &checker.Result{Status: "up"}
		got := evaluateAssertions(mon, result)
		if got != "up" {
			t.Fatalf("expected up, got %s", got)
		}
	})

	t.Run("failing hard assertion returns down", func(t *testing.T) {
		assertions := `{"operator":"and","groups":[{"operator":"and","conditions":[{"type":"status_code","operator":"eq","value":"200"}]}]}`
		mon := &storage.Monitor{Assertions: json.RawMessage(assertions)}
		result := &checker.Result{Status: "up", StatusCode: 500}
		got := evaluateAssertions(mon, result)
		if got != "down" {
			t.Fatalf("expected down, got %s", got)
		}
	})

	t.Run("failing soft assertion returns degraded", func(t *testing.T) {
		assertions := `{"operator":"and","groups":[{"operator":"and","conditions":[{"type":"status_code","operator":"eq","value":"200","degraded":true}]}]}`
		mon := &storage.Monitor{Assertions: json.RawMessage(assertions)}
		result := &checker.Result{Status: "up", StatusCode: 500}
		got := evaluateAssertions(mon, result)
		if got != "degraded" {
			t.Fatalf("expected degraded, got %s", got)
		}
	})

	t.Run("passing assertion keeps status", func(t *testing.T) {
		assertions := `{"operator":"and","groups":[{"operator":"and","conditions":[{"type":"status_code","operator":"eq","value":"200"}]}]}`
		mon := &storage.Monitor{Assertions: json.RawMessage(assertions)}
		result := &checker.Result{Status: "up", StatusCode: 200}
		got := evaluateAssertions(mon, result)
		if got != "up" {
			t.Fatalf("expected up, got %s", got)
		}
	})
}

func TestBuildCheckResult(t *testing.T) {
	now := time.Now()
	certTs := now.Unix()
	mon := &storage.Monitor{ID: 42}
	result := &checker.Result{
		Status:       "up",
		ResponseTime: 150,
		StatusCode:   200,
		Message:      "OK",
		Headers:      map[string]string{"Content-Type": "text/html"},
		Body:         "<html>",
		BodyHash:     "abc123",
		CertExpiry:   &certTs,
		DNSRecords:   []string{"1.2.3.4"},
	}

	cr := buildCheckResult(mon, result, "up")

	if cr.MonitorID != 42 {
		t.Fatalf("expected monitor_id 42, got %d", cr.MonitorID)
	}
	if cr.Status != "up" {
		t.Fatalf("expected status up, got %s", cr.Status)
	}
	if cr.ResponseTime != 150 {
		t.Fatalf("expected response_time 150, got %d", cr.ResponseTime)
	}
	if cr.StatusCode != 200 {
		t.Fatalf("expected status_code 200, got %d", cr.StatusCode)
	}
	if cr.Message != "OK" {
		t.Fatalf("expected message OK, got %s", cr.Message)
	}
	if cr.BodyHash != "abc123" {
		t.Fatalf("expected body_hash abc123, got %s", cr.BodyHash)
	}
	if cr.CertExpiry == nil {
		t.Fatal("expected cert_expiry to be set")
	}
	if cr.Body != "<html>" {
		t.Fatalf("expected body <html>, got %s", cr.Body)
	}
}

func TestEmitNotification(t *testing.T) {
	logger := discardLogger()
	store := testStore(t)
	registry := checker.NewRegistry()
	incMgr := incident.NewManager(store, logger)
	p := NewPipeline(store, registry, incMgr, 1, false, logger)

	t.Run("event lands on channel", func(t *testing.T) {
		mon := &storage.Monitor{ID: 1, Name: "test"}
		p.emitNotification("incident.created", nil, mon, nil)

		select {
		case ev := <-p.notifyChan:
			if ev.EventType != "incident.created" {
				t.Fatalf("expected incident.created, got %s", ev.EventType)
			}
			if ev.MonitorID != 1 {
				t.Fatalf("expected monitor_id 1, got %d", ev.MonitorID)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for notification")
		}
	})

	t.Run("full channel increments dropped counter", func(t *testing.T) {
		smallStore := testStore(t)
		smallIncMgr := incident.NewManager(smallStore, logger)
		smallRegistry := checker.NewRegistry()
		smallP := NewPipeline(smallStore, smallRegistry, smallIncMgr, 1, false, logger)
		// Replace notifyChan with a size-1 channel and fill it
		smallP.notifyChan = make(chan NotificationEvent, 1)
		smallP.notifyChan <- NotificationEvent{}
		before := smallP.droppedNotifications.Load()

		mon := &storage.Monitor{ID: 2}
		smallP.emitNotification("test", nil, mon, nil)

		after := smallP.droppedNotifications.Load()
		if after <= before {
			t.Fatal("expected dropped counter to increment")
		}
	})
}

func TestHandleResult(t *testing.T) {
	logger := discardLogger()
	store := testStore(t)
	ctx := context.Background()

	mon := &storage.Monitor{
		Name:             "Test HTTP",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	registry := checker.NewRegistry()
	incMgr := incident.NewManager(store, logger)
	p := NewPipeline(store, registry, incMgr, 1, false, logger)

	t.Run("inserts check result and updates status", func(t *testing.T) {
		wr := WorkerResult{
			Monitor: mon,
			Result:  &checker.Result{Status: "up", ResponseTime: 100, StatusCode: 200},
		}
		p.handleResult(ctx, wr)

		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "up" {
			t.Fatalf("expected up, got %s", status.Status)
		}
		if status.ConsecSuccesses != 1 {
			t.Fatalf("expected 1 consec success, got %d", status.ConsecSuccesses)
		}
	})

	t.Run("consecutive fails increment", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			wr := WorkerResult{
				Monitor: mon,
				Result:  &checker.Result{Status: "down", Message: "timeout"},
			}
			p.handleResult(ctx, wr)
		}

		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.ConsecFails != 3 {
			t.Fatalf("expected 3 consec fails, got %d", status.ConsecFails)
		}
		if status.ConsecSuccesses != 0 {
			t.Fatalf("expected 0 consec successes, got %d", status.ConsecSuccesses)
		}
	})

	t.Run("success resets fail counter", func(t *testing.T) {
		wr := WorkerResult{
			Monitor: mon,
			Result:  &checker.Result{Status: "up", ResponseTime: 50},
		}
		p.handleResult(ctx, wr)

		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.ConsecFails != 0 {
			t.Fatalf("expected 0 consec fails, got %d", status.ConsecFails)
		}
		if status.ConsecSuccesses != 1 {
			t.Fatalf("expected 1 consec success, got %d", status.ConsecSuccesses)
		}
	})
}

func TestProcessIncidents(t *testing.T) {
	logger := discardLogger()
	store := testStore(t)
	ctx := context.Background()

	mon := &storage.Monitor{
		Name:             "Incident Test",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 2,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	registry := checker.NewRegistry()
	incMgr := incident.NewManager(store, logger)
	p := NewPipeline(store, registry, incMgr, 1, false, logger)

	t.Run("incident created after threshold failures", func(t *testing.T) {
		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			ConsecFails: 2,
		}
		p.processIncidents(ctx, mon, "down", status, "connection refused")

		inc, err := store.GetOpenIncident(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if inc == nil {
			t.Fatal("expected incident to be created")
		}
		if inc.Cause != "connection refused" {
			t.Fatalf("expected cause 'connection refused', got %q", inc.Cause)
		}

		// Drain notification channel
		select {
		case <-p.notifyChan:
		case <-time.After(time.Second):
			t.Fatal("expected notification event")
		}
	})

	t.Run("incident resolved after threshold successes", func(t *testing.T) {
		status := &storage.MonitorStatus{
			MonitorID:       mon.ID,
			ConsecSuccesses: 1,
		}
		p.processIncidents(ctx, mon, "up", status, "")

		inc, err := store.GetOpenIncident(ctx, mon.ID)
		if err != nil {
			// sql.ErrNoRows is expected here
		}
		if inc != nil {
			t.Fatal("expected no open incident after resolution")
		}

		select {
		case <-p.notifyChan:
		case <-time.After(time.Second):
			t.Fatal("expected resolution notification")
		}
	})

	t.Run("maintenance suppresses notification", func(t *testing.T) {
		// Create a maintenance window covering now
		now := time.Now()
		mw := &storage.MaintenanceWindow{
			Name:      "Test Maintenance",
			StartTime: now.Add(-1 * time.Hour),
			EndTime:   now.Add(1 * time.Hour),
		}
		if err := store.CreateMaintenanceWindow(ctx, mw); err != nil {
			t.Fatal(err)
		}

		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			ConsecFails: 2,
		}
		p.processIncidents(ctx, mon, "down", status, "maintenance test")

		// Should still create incident but NOT emit notification
		select {
		case <-p.notifyChan:
			t.Fatal("expected no notification during maintenance")
		case <-time.After(100 * time.Millisecond):
			// good — no notification
		}
	})
}

func TestResendNotificationInterval(t *testing.T) {
	logger := discardLogger()
	store := testStore(t)
	ctx := context.Background()

	mon := &storage.Monitor{
		Name:             "Resend Test",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 2,
		SuccessThreshold: 1,
		ResendInterval:   10,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	registry := checker.NewRegistry()
	incMgr := incident.NewManager(store, logger)
	p := NewPipeline(store, registry, incMgr, 1, false, logger)

	drainNotifications := func() {
		for {
			select {
			case <-p.notifyChan:
			default:
				return
			}
		}
	}

	t.Run("initial failure sends incident.created", func(t *testing.T) {
		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			ConsecFails: 2,
		}
		p.processIncidents(ctx, mon, "down", status, "connection refused")

		select {
		case ev := <-p.notifyChan:
			if ev.EventType != "incident.created" {
				t.Fatalf("expected incident.created, got %s", ev.EventType)
			}
		case <-time.After(time.Second):
			t.Fatal("expected notification event")
		}
	})

	t.Run("subsequent failure before interval sends nothing", func(t *testing.T) {
		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			ConsecFails: 3,
		}
		p.processIncidents(ctx, mon, "down", status, "still down")

		select {
		case ev := <-p.notifyChan:
			t.Fatalf("expected no notification before resend interval, got %s", ev.EventType)
		case <-time.After(100 * time.Millisecond):
			// good — no notification
		}
	})

	t.Run("subsequent failure after interval sends incident.reminder", func(t *testing.T) {
		// Manually backdate the lastNotified timestamp
		p.lastNotified.Store(mon.ID, time.Now().Add(-11*time.Second))

		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			ConsecFails: 4,
		}
		p.processIncidents(ctx, mon, "down", status, "still down")

		select {
		case ev := <-p.notifyChan:
			if ev.EventType != "incident.reminder" {
				t.Fatalf("expected incident.reminder, got %s", ev.EventType)
			}
		case <-time.After(time.Second):
			t.Fatal("expected reminder notification")
		}
	})

	t.Run("resend disabled when interval is zero", func(t *testing.T) {
		drainNotifications()

		noResendMon := &storage.Monitor{
			Name:             "No Resend",
			Type:             "http",
			Target:           "https://example2.com",
			Interval:         60,
			Timeout:          10,
			Enabled:          true,
			FailureThreshold: 1,
			SuccessThreshold: 1,
			ResendInterval:   0,
		}
		if err := store.CreateMonitor(ctx, noResendMon); err != nil {
			t.Fatal(err)
		}

		// Create incident first
		status := &storage.MonitorStatus{
			MonitorID:   noResendMon.ID,
			ConsecFails: 1,
		}
		p.processIncidents(ctx, noResendMon, "down", status, "initial failure")
		drainNotifications()

		// Subsequent failure should NOT resend
		status.ConsecFails = 2
		p.processIncidents(ctx, noResendMon, "down", status, "still down")

		select {
		case ev := <-p.notifyChan:
			t.Fatalf("expected no notification with resend_interval=0, got %s", ev.EventType)
		case <-time.After(100 * time.Millisecond):
			// good
		}
	})

	t.Run("recovery clears resend tracking", func(t *testing.T) {
		drainNotifications()

		status := &storage.MonitorStatus{
			MonitorID:       mon.ID,
			ConsecSuccesses: 1,
		}
		p.processIncidents(ctx, mon, "up", status, "")
		drainNotifications()

		// Verify lastNotified was cleared
		_, exists := p.lastNotified.Load(mon.ID)
		if exists {
			t.Fatal("expected lastNotified to be cleared after recovery")
		}
	})

	t.Run("maintenance suppresses resend", func(t *testing.T) {
		drainNotifications()

		now := time.Now()
		mw := &storage.MaintenanceWindow{
			Name:      "Resend Maintenance",
			StartTime: now.Add(-1 * time.Hour),
			EndTime:   now.Add(1 * time.Hour),
		}
		if err := store.CreateMaintenanceWindow(ctx, mw); err != nil {
			t.Fatal(err)
		}

		// Create fresh incident
		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			ConsecFails: 2,
		}
		p.processIncidents(ctx, mon, "down", status, "maint failure")
		drainNotifications()

		// Backdate to trigger resend
		p.lastNotified.Store(mon.ID, time.Now().Add(-11*time.Second))
		status.ConsecFails = 3
		p.processIncidents(ctx, mon, "down", status, "maint still down")

		select {
		case ev := <-p.notifyChan:
			t.Fatalf("expected no resend during maintenance, got %s", ev.EventType)
		case <-time.After(100 * time.Millisecond):
			// good
		}
	})
}

func TestShouldResend(t *testing.T) {
	logger := discardLogger()
	store := testStore(t)
	registry := checker.NewRegistry()
	incMgr := incident.NewManager(store, logger)
	p := NewPipeline(store, registry, incMgr, 1, false, logger)

	t.Run("returns false when resend_interval is zero", func(t *testing.T) {
		mon := &storage.Monitor{ID: 1, ResendInterval: 0}
		if p.shouldResend(mon) {
			t.Fatal("expected false for zero interval")
		}
	})

	t.Run("returns false when resend_interval is negative", func(t *testing.T) {
		mon := &storage.Monitor{ID: 2, ResendInterval: -1}
		if p.shouldResend(mon) {
			t.Fatal("expected false for negative interval")
		}
	})

	t.Run("returns true when never notified", func(t *testing.T) {
		mon := &storage.Monitor{ID: 3, ResendInterval: 60}
		if !p.shouldResend(mon) {
			t.Fatal("expected true when never notified")
		}
	})

	t.Run("returns false when recently notified", func(t *testing.T) {
		mon := &storage.Monitor{ID: 4, ResendInterval: 60}
		p.lastNotified.Store(mon.ID, time.Now())
		if p.shouldResend(mon) {
			t.Fatal("expected false when recently notified")
		}
	})

	t.Run("returns true when interval elapsed", func(t *testing.T) {
		mon := &storage.Monitor{ID: 5, ResendInterval: 10}
		p.lastNotified.Store(mon.ID, time.Now().Add(-11*time.Second))
		if !p.shouldResend(mon) {
			t.Fatal("expected true when interval elapsed")
		}
	})
}

func TestSchedulerDispatch(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := discardLogger()

	// Create an enabled monitor
	mon := &storage.Monitor{
		Name:             "Scheduled",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	// Create a heartbeat monitor (should be skipped)
	hbMon := &storage.Monitor{
		Name:             "Heartbeat",
		Type:             "heartbeat",
		Target:           "heartbeat",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, hbMon); err != nil {
		t.Fatal(err)
	}

	jobs := make(chan Job, 10)
	s := NewScheduler(store, jobs, logger)
	s.loadMonitors(ctx)

	t.Run("due monitors dispatched", func(t *testing.T) {
		now := time.Now().Add(time.Minute)
		s.dispatch(now)

		select {
		case job := <-jobs:
			if job.Monitor.ID != mon.ID {
				t.Fatalf("expected monitor %d, got %d", mon.ID, job.Monitor.ID)
			}
		case <-time.After(time.Second):
			t.Fatal("expected job to be dispatched")
		}
	})

	t.Run("manual monitors skipped", func(t *testing.T) {
		for len(jobs) > 0 {
			<-jobs
		}
		manualMon := &storage.Monitor{
			Name:             "Manual",
			Type:             "manual",
			Target:           "manual",
			Interval:         60,
			Timeout:          10,
			Enabled:          true,
			FailureThreshold: 3,
			SuccessThreshold: 1,
		}
		if err := store.CreateMonitor(ctx, manualMon); err != nil {
			t.Fatal(err)
		}
		s.loadMonitors(ctx)
		now := time.Now().Add(3 * time.Minute)
		s.dispatch(now)

		for len(jobs) > 0 {
			job := <-jobs
			if job.Monitor.Type == "manual" {
				t.Fatal("manual monitor should not be dispatched")
			}
		}
	})

	t.Run("heartbeat monitors skipped", func(t *testing.T) {
		// Drain and re-dispatch
		for len(jobs) > 0 {
			<-jobs
		}
		now := time.Now().Add(5 * time.Minute)
		s.dispatch(now)

		found := false
		for len(jobs) > 0 {
			job := <-jobs
			if job.Monitor.Type == "heartbeat" {
				t.Fatal("heartbeat monitor should not be dispatched")
			}
			found = true
		}
		if !found {
			t.Fatal("expected at least one job dispatched")
		}
	})

	t.Run("full channel increments dropped jobs", func(t *testing.T) {
		fullJobs := make(chan Job) // unbuffered = full
		fullSched := NewScheduler(store, fullJobs, logger)
		fullSched.loadMonitors(ctx)

		before := fullSched.droppedJobs.Load()
		fullSched.dispatch(time.Now().Add(10 * time.Minute))
		after := fullSched.droppedJobs.Load()

		if after <= before {
			t.Fatal("expected dropped jobs to increment")
		}
	})
}

func TestSchedulerHeapOrdering(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := discardLogger()

	// Create two monitors with different intervals
	fast := &storage.Monitor{
		Name:             "Fast",
		Type:             "http",
		Target:           "https://fast.example.com",
		Interval:         10,
		Timeout:          5,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, fast); err != nil {
		t.Fatal(err)
	}

	slow := &storage.Monitor{
		Name:             "Slow",
		Type:             "http",
		Target:           "https://slow.example.com",
		Interval:         300,
		Timeout:          5,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, slow); err != nil {
		t.Fatal(err)
	}

	jobs := make(chan Job, 10)
	s := NewScheduler(store, jobs, logger)
	s.loadMonitors(ctx)

	// First dispatch: both are due (nextRun = now)
	now := time.Now().Add(time.Minute)
	s.dispatch(now)

	// Drain all jobs from first dispatch
	for len(jobs) > 0 {
		<-jobs
	}

	// After first dispatch, fast should fire again at now+10s,
	// slow at now+300s. Dispatch at now+15s should only fire fast.
	s.dispatch(now.Add(15 * time.Second))

	select {
	case job := <-jobs:
		if job.Monitor.ID != fast.ID {
			t.Fatalf("expected fast monitor %d to fire first, got %d", fast.ID, job.Monitor.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected fast monitor to be dispatched")
	}

	// Slow should not have fired
	select {
	case job := <-jobs:
		t.Fatalf("slow monitor %d should not have fired, got job for %d", slow.ID, job.Monitor.ID)
	default:
		// good
	}
}

func TestSchedulerUpdateInterval(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := discardLogger()

	mon := &storage.Monitor{
		Name:             "Adaptive",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	jobs := make(chan Job, 10)
	s := NewScheduler(store, jobs, logger)
	s.loadMonitors(ctx)

	t.Run("default multiplier is 1.0", func(t *testing.T) {
		m := s.GetMultiplier(mon.ID)
		if m != 1.0 {
			t.Fatalf("expected default multiplier 1.0, got %v", m)
		}
	})

	t.Run("update interval changes effective interval", func(t *testing.T) {
		newInterval := 120 * time.Second
		s.UpdateInterval(mon.ID, newInterval)

		m := s.GetMultiplier(mon.ID)
		want := 2.0
		if m != want {
			t.Fatalf("expected multiplier %v after doubling interval, got %v", want, m)
		}
	})

	t.Run("updated interval affects dispatch timing", func(t *testing.T) {
		// Set effective interval to 120s
		s.UpdateInterval(mon.ID, 120*time.Second)

		// First dispatch to consume the initial due entry
		now := time.Now().Add(time.Minute)
		s.dispatch(now)
		for len(jobs) > 0 {
			<-jobs
		}

		// Dispatch at now+60s: with 120s interval, should NOT fire
		s.dispatch(now.Add(60 * time.Second))
		select {
		case <-jobs:
			t.Fatal("monitor should not fire before updated interval elapses")
		default:
			// good
		}

		// Dispatch at now+121s: should fire
		s.dispatch(now.Add(121 * time.Second))
		select {
		case <-jobs:
			// good
		default:
			t.Fatal("monitor should fire after updated interval elapses")
		}
	})

	t.Run("get multiplier for unknown monitor returns 1.0", func(t *testing.T) {
		m := s.GetMultiplier(99999)
		if m != 1.0 {
			t.Fatalf("expected 1.0 for unknown monitor, got %v", m)
		}
	})
}

func TestSchedulerReload(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	logger := discardLogger()

	mon := &storage.Monitor{
		Name:             "Reloadable",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	jobs := make(chan Job, 10)
	s := NewScheduler(store, jobs, logger)
	s.loadMonitors(ctx)

	// Set a custom effective interval
	s.UpdateInterval(mon.ID, 90*time.Second)

	// Reload should preserve effective interval
	s.loadMonitors(ctx)

	s.mu.Lock()
	eff := s.effectiveInterval[mon.ID]
	s.mu.Unlock()

	if eff != int64(90*time.Second) {
		t.Fatalf("expected effective interval preserved after reload (90s), got %v", time.Duration(eff))
	}

	// Disable the monitor and reload
	if err := store.SetMonitorEnabled(ctx, mon.ID, false); err != nil {
		t.Fatal(err)
	}
	s.loadMonitors(ctx)

	s.mu.Lock()
	_, exists := s.effectiveInterval[mon.ID]
	heapLen := s.heap.Len()
	s.mu.Unlock()

	if exists {
		t.Fatal("expected effective interval to be removed for disabled monitor")
	}
	if heapLen != 0 {
		t.Fatalf("expected empty heap after disabling only monitor, got %d entries", heapLen)
	}
}

func TestProcessManualStatus(t *testing.T) {
	logger := discardLogger()
	store := testStore(t)
	ctx := context.Background()

	mon := &storage.Monitor{
		Name:             "Manual Test",
		Type:             "manual",
		Target:           "manual",
		Interval:         60,
		Timeout:          10,
		Enabled:          true,
		FailureThreshold: 1,
		SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	registry := checker.NewRegistry()
	incMgr := incident.NewManager(store, logger)
	p := NewPipeline(store, registry, incMgr, 1, false, logger)

	drainNotifications := func() {
		for {
			select {
			case <-p.notifyChan:
			default:
				return
			}
		}
	}

	t.Run("set down creates incident", func(t *testing.T) {
		p.ProcessManualStatus(ctx, mon, "down", "manual outage")

		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "down" {
			t.Fatalf("expected down, got %s", status.Status)
		}
		if status.ConsecFails != mon.FailureThreshold {
			t.Fatalf("expected consec_fails=%d, got %d", mon.FailureThreshold, status.ConsecFails)
		}

		inc, err := store.GetOpenIncident(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if inc == nil {
			t.Fatal("expected incident to be created")
		}

		select {
		case ev := <-p.notifyChan:
			if ev.EventType != "incident.created" {
				t.Fatalf("expected incident.created, got %s", ev.EventType)
			}
		case <-time.After(time.Second):
			t.Fatal("expected notification event")
		}
	})

	t.Run("set up resolves incident", func(t *testing.T) {
		drainNotifications()
		p.ProcessManualStatus(ctx, mon, "up", "back online")

		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "up" {
			t.Fatalf("expected up, got %s", status.Status)
		}
		if status.ConsecSuccesses != mon.SuccessThreshold {
			t.Fatalf("expected consec_successes=%d, got %d", mon.SuccessThreshold, status.ConsecSuccesses)
		}

		inc, _ := store.GetOpenIncident(ctx, mon.ID)
		if inc != nil {
			t.Fatal("expected no open incident after recovery")
		}

		select {
		case ev := <-p.notifyChan:
			if ev.EventType != "incident.resolved" {
				t.Fatalf("expected incident.resolved, got %s", ev.EventType)
			}
		case <-time.After(time.Second):
			t.Fatal("expected resolution notification")
		}
	})

	t.Run("set degraded creates incident", func(t *testing.T) {
		drainNotifications()
		p.ProcessManualStatus(ctx, mon, "degraded", "partial outage")

		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "degraded" {
			t.Fatalf("expected degraded, got %s", status.Status)
		}

		select {
		case ev := <-p.notifyChan:
			if ev.EventType != "incident.created" {
				t.Fatalf("expected incident.created, got %s", ev.EventType)
			}
		case <-time.After(time.Second):
			t.Fatal("expected notification event")
		}
	})

	t.Run("inserts check result", func(t *testing.T) {
		drainNotifications()

		beforeChecks, err := store.ListCheckResults(ctx, mon.ID, storage.Pagination{Page: 1, PerPage: 100})
		if err != nil {
			t.Fatal(err)
		}
		countBefore := beforeChecks.Total

		p.ProcessManualStatus(ctx, mon, "up", "test check result")
		drainNotifications()

		afterChecks, err := store.ListCheckResults(ctx, mon.ID, storage.Pagination{Page: 1, PerPage: 100})
		if err != nil {
			t.Fatal(err)
		}
		if afterChecks.Total <= countBefore {
			t.Fatalf("expected check result count to increase, before=%d after=%d", countBefore, afterChecks.Total)
		}
	})
}

package monitor

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/checker"
	"github.com/y0f/asura/v3/internal/incident"
	"github.com/y0f/asura/v3/internal/storage"
)

type heldIncidentReadStore struct {
	*storage.SQLiteStore
	reads   atomic.Int64
	entered chan struct{}
	release chan struct{}
}

func (s *heldIncidentReadStore) RecordIncidentFailure(ctx context.Context, id int64, severity, cause string) (*storage.Incident, bool, error) {
	if s.reads.Add(1) <= 2 {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	return s.SQLiteStore.RecordIncidentFailure(ctx, id, severity, cause)
}

func TestConcurrentManualFailureCreatesOneIncident(t *testing.T) {
	db := testStore(t)
	s := &heldIncidentReadStore{SQLiteStore: db, entered: make(chan struct{}, 2), release: make(chan struct{})}
	logger := discardLogger()
	p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "manual"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	go func() { p.ProcessManualStatus(ctx, mon, "down", "first"); done <- struct{}{} }()
	select {
	case <-s.entered:
	case <-ctx.Done():
		t.Fatal("first failure never reached incident lookup")
	}
	go func() { p.ProcessManualStatus(ctx, mon, "down", "second"); done <- struct{}{} }()
	// Hold the complete incident transaction so a second caller can reach it.
	// Correct serialization keeps it waiting until the first transition finishes.
	select {
	case <-s.entered:
	case <-time.After(50 * time.Millisecond):
	}
	close(s.release)
	for range 2 {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("manual transition did not finish")
		}
	}
	incidents, err := db.ListIncidents(ctx, mon.ID, "open", "", storage.Pagination{Page: 1, PerPage: 10})
	if err != nil || incidents.Total != 1 {
		t.Fatalf("concurrent failures produced duplicate incidents: %+v %v", incidents, err)
	}
	if events := drainEvents(p); countEvents(events, "incident.created") != 1 {
		t.Fatalf("expected exactly one creation notification: %v", events)
	}
}

func TestManualFailureRaisesExistingSeverity(t *testing.T) {
	s, p := newCountingPipeline(t)
	ctx := context.Background()
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "manual"})
	p.ProcessManualStatus(ctx, mon, "degraded", "slow")
	first, err := s.GetOpenIncident(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.ProcessManualStatus(ctx, mon, "down", "unavailable")
	got, err := s.GetOpenIncident(ctx, mon.ID)
	if err != nil || got.ID != first.ID || got.Severity != "critical" {
		t.Fatalf("manual failure did not raise same incident: %+v %v", got, err)
	}
	p.ProcessManualStatus(ctx, mon, "degraded", "slow again")
	got, err = s.GetOpenIncident(ctx, mon.ID)
	if err != nil || got.Severity != "critical" {
		t.Fatalf("severity was lowered: %+v %v", got, err)
	}
	p.ProcessManualStatus(ctx, mon, "up", "recovered")
	if _, err := s.GetOpenIncident(ctx, mon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("recovered manual incident remains open: %v", err)
	}
}

type unavailableManualStatusStore struct{ *storage.SQLiteStore }

func (s *unavailableManualStatusStore) RecordManualStatus(context.Context, int64, string, string, ...storage.MonitorAccess) (*storage.Monitor, error) {
	return nil, errors.New("injected transition failure")
}

func TestManualStatusPersistenceFailureHasNoIncidentEffects(t *testing.T) {
	s := &unavailableManualStatusStore{SQLiteStore: testStore(t)}
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "manual"})
	logger := discardLogger()
	p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
	ctx := context.Background()
	if err := p.ProcessManualStatus(ctx, mon, "down", "failure"); err == nil {
		t.Fatal("transition failure reported success")
	}
	got, err := s.SQLiteStore.GetMonitorStatus(ctx, mon.ID)
	if err != nil || got.Status != "pending" {
		t.Fatalf("failed transition changed monitor status: %+v %v", got, err)
	}
	if _, err := s.GetOpenIncident(ctx, mon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed transition created an incident: %v", err)
	}
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("failed transition emitted events: %v", events)
	}
}

func TestManualStatusUsesCurrentConfigurationAndScope(t *testing.T) {
	s, p := newCountingPipeline(t)
	ctx := context.Background()
	stale := createTestMonitor(t, s, &storage.Monitor{Type: "manual", Name: "before"})
	current := *stale
	current.Name = "after"
	current.FailureThreshold = 7
	if err := s.UpdateMonitor(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessManualStatus(ctx, stale, "down", "denied", storage.MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pipeline omitted the supplied deny filter: %v", err)
	}
	if _, err := s.GetOpenIncident(ctx, stale.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("denied transition created incident: %v", err)
	}
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("denied transition emitted events: %v", events)
	}
	if err := p.ProcessManualStatus(ctx, stale, "down", "authorized", storage.MonitorAccess{MonitorIDs: []int64{stale.ID}}); err != nil {
		t.Fatal(err)
	}
	status, err := s.GetMonitorStatus(ctx, stale.ID)
	if err != nil || status.ConsecFails != 7 {
		t.Fatalf("used stale thresholds: %+v %v", status, err)
	}
	select {
	case ev := <-p.notifyChan:
		if ev.EventType != "incident.created" || ev.Monitor == nil || ev.Monitor.Name != "after" {
			t.Fatalf("notification used stale configuration: %+v", ev)
		}
	default:
		t.Fatal("no incident notification")
	}
}

type failingIncidentStore struct {
	*countingStore
	failures int
}

func (s *failingIncidentStore) RecordIncidentFailure(ctx context.Context, id int64, severity, cause string) (*storage.Incident, bool, error) {
	if s.failures > 0 {
		s.failures--
		return nil, false, errors.New("injected incident creation failure")
	}
	return s.SQLiteStore.RecordIncidentFailure(ctx, id, severity, cause)
}

func TestHeartbeatReminderSkipsChangedMonitorType(t *testing.T) {
	s, p := newCountingPipeline(t)
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "http"})
	// This candidate was collected before the type changed. A stale heartbeat
	// record must not open an incident or send reminders for the HTTP monitor.
	s.expired = []*storage.Heartbeat{{MonitorID: mon.ID, Status: "down"}}
	watcher := NewHeartbeatWatcher(s, incident.NewManager(s, discardLogger()), p, time.Second, discardLogger())
	watcher.check(context.Background())
	if calls := s.openIncCalls.Load(); calls != 0 {
		t.Fatalf("stale heartbeat evaluated incident state %d times", calls)
	}
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("stale heartbeat sent events: %v", events)
	}
}

func TestHeartbeatRetriesIncidentAfterCommittedExpiry(t *testing.T) {
	base, _ := newCountingPipeline(t)
	s := &failingIncidentStore{countingStore: base, failures: 1}
	ctx := context.Background()
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "heartbeat", ResendInterval: 0})
	if err := s.CreateHeartbeat(ctx, &storage.Heartbeat{MonitorID: mon.ID, Token: "retry", Status: "down"}); err != nil {
		t.Fatal(err)
	}
	// Model a restart after the expiry committed but before incident creation.
	last := time.Now().Add(-time.Hour)
	s.expired = []*storage.Heartbeat{{MonitorID: mon.ID, Status: "down", LastPingAt: &last}}
	logger := discardLogger()
	mgr := incident.NewManager(s, logger)
	p := NewPipeline(s, checker.NewRegistry(), mgr, 1, false, logger)
	w := NewHeartbeatWatcher(s, mgr, p, time.Second, logger)
	w.check(ctx)
	if _, err := s.SQLiteStore.GetOpenIncident(ctx, mon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected injected failure without incident: %v", err)
	}
	w.check(ctx)
	if _, err := s.SQLiteStore.GetOpenIncident(ctx, mon.ID); err != nil {
		t.Fatalf("already-down heartbeat did not retry incident creation: %v", err)
	}
	if events := drainEvents(p); countEvents(events, "incident.created") != 1 {
		t.Fatalf("retry did not announce exactly once: %v", events)
	}
}

type heldHeartbeatExpiryStore struct {
	*storage.SQLiteStore
	expired chan struct{}
	release chan struct{}
}

func (s *heldHeartbeatExpiryStore) ExpireHeartbeat(ctx context.Context, id int64) (bool, error) {
	won, err := s.SQLiteStore.ExpireHeartbeat(ctx, id)
	if won {
		close(s.expired)
		select {
		case <-s.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return won, err
}

func TestHeartbeatPingDuringExpiryRecoversIncident(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heartbeat.db")
	db, err := storage.NewSQLiteStore(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &heldHeartbeatExpiryStore{SQLiteStore: db, expired: make(chan struct{}), release: make(chan struct{})}
	logger := discardLogger()
	mgr := incident.NewManager(s, logger)
	p := NewPipeline(s, checker.NewRegistry(), mgr, 1, false, logger)
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "heartbeat"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.CreateHeartbeat(ctx, &storage.Heartbeat{MonitorID: mon.ID, Token: "heartbeat-race", Status: "up"}); err != nil {
		t.Fatal(err)
	}
	fixture, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.ExecContext(ctx, `UPDATE heartbeats SET last_ping_at=? WHERE monitor_id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), mon.ID)
	fixture.Close()
	if err != nil {
		t.Fatal(err)
	}
	watcher := NewHeartbeatWatcher(s, mgr, p, time.Second, logger)
	watchDone := make(chan struct{})
	go func() { watcher.check(ctx); close(watchDone) }()
	select {
	case <-s.expired:
	case <-ctx.Done():
		t.Fatal("watcher never committed expiry")
	}
	pingDone := make(chan error, 1)
	go func() { pingDone <- p.ProcessHeartbeatPing(ctx, mon.ID, "heartbeat-race") }()
	select {
	case err := <-pingDone:
		close(s.release)
		<-watchDone
		t.Fatalf("ping overtook unfinished expiry/incident transition: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(s.release)
	if err := <-pingDone; err != nil {
		t.Fatal(err)
	}
	<-watchDone
	hb, err := s.GetHeartbeatByMonitorID(ctx, mon.ID)
	if err != nil || hb.Status != "up" {
		t.Fatalf("ping did not recover heartbeat: %+v %v", hb, err)
	}
	status, err := s.GetMonitorStatus(ctx, mon.ID)
	if err != nil || status.Status != "up" {
		t.Fatalf("ping did not recover dashboard: %+v %v", status, err)
	}
	if _, err := s.GetOpenIncident(ctx, mon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ping left an open incident: %v", err)
	}
	if events := drainEvents(p); len(events) != 2 || events[0] != "incident.created" || events[1] != "incident.resolved" {
		t.Fatalf("inconsistent transition notifications: %v", events)
	}
}

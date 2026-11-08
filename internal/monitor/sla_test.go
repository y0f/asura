package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

type slaBudgetStore struct {
	*countingStore
	total, up int64
	reads     int
	fail      error
}

func (s *slaBudgetStore) GetMonitorSLASnapshot(ctx context.Context, id int64, _, _, _ time.Time, _ ...storage.MonitorAccess) (*storage.MonitorSLASnapshot, error) {
	s.reads++
	m, err := s.GetMonitor(ctx, id)
	if err != nil {
		return nil, err
	}
	return &storage.MonitorSLASnapshot{Sampled: m.HasSampledMetrics(), Target: m.SLATarget, Period: []*storage.DailyUptime{{TotalChecks: s.total, UpChecks: s.up, DownChecks: s.total - s.up}}}, s.fail
}

func slaPipelineFixture(t *testing.T) (*slaBudgetStore, *Pipeline, *storage.Monitor) {
	t.Helper()
	base, _ := newCountingPipeline(t)
	s := &slaBudgetStore{countingStore: base, total: 10000, up: 9990}
	logger := discardLogger()
	p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
	m := createTestMonitor(t, s, &storage.Monitor{Type: "http", SLATarget: 99, Enabled: true})
	return s, p, m
}

func TestPassiveSLATargetNeverEvaluatesTransitionCounts(t *testing.T) {
	s, p, m := slaPipelineFixture(t)
	s.total, s.up = 2, 1
	for _, kind := range []string{"manual", "heartbeat"} {
		m.Type = kind
		p.checkSLABreachAt(context.Background(), m, time.Now())
		if s.reads != 0 || len(drainEvents(p)) != 0 {
			t.Fatal("passive state changes caused an SLA alert")
		}
	}
}

func TestSLABudgetReevaluatesAndEscalatesWithinExistingOutage(t *testing.T) {
	s, p, m := slaPipelineFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	p.checkSLABreachAt(ctx, m, now)
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("healthy budget alerted: %v", events)
	}
	s.up = 9820
	p.checkSLABreachAt(ctx, m, now.Add(30*time.Second))
	if s.reads != 1 {
		t.Fatalf("repeated checks rescanned monthly history: %d", s.reads)
	}
	p.checkSLABreachAt(ctx, m, now.Add(time.Minute))
	if events := drainEvents(p); countEvents(events, "sla.breach") != 1 {
		t.Fatalf("continued outage missed risk threshold: %v", events)
	}
	s.up = 9799
	p.checkSLABreachAt(ctx, m, now.Add(2*time.Minute))
	if events := drainEvents(p); countEvents(events, "sla.breach") != 1 {
		t.Fatalf("breach was hidden by warning dedupe: %v", events)
	}
	p.checkSLABreachAt(ctx, m, now.Add(3*time.Minute))
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("duplicate breach alert: %v", events)
	}
	p.checkSLABreachAt(ctx, m, now.Add(62*time.Minute))
	if events := drainEvents(p); countEvents(events, "sla.breach") != 1 {
		t.Fatalf("hourly reminder missing: %v", events)
	}
	p.checkSLABreachAt(ctx, m, time.Date(2026, 12, 16, 0, 0, 0, 0, time.UTC))
	if events := drainEvents(p); countEvents(events, "sla.breach") != 1 {
		t.Fatalf("new month retained stale dedupe: %v", events)
	}
}

func TestSLABudgetNoDataMaintenanceAndFailedQueue(t *testing.T) {
	s, p, m := slaPipelineFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	m.SLATarget = 100
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	s.total, s.up = 0, 0
	p.checkSLABreachAt(ctx, m, now)
	s.total, s.up = 1000, 1000
	p.checkSLABreachAt(ctx, m, now.Add(time.Minute))
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("zero allowance without downtime alerted: %v", events)
	}
	s.up = 0
	s.maintenanceFunc = func(int64) bool { return true }
	p.checkSLABreachAt(ctx, m, now.Add(2*time.Minute))
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("maintenance did not suppress SLA: %v", events)
	}
	s.maintenanceFunc = func(int64) bool { return false }
	s.fail = errors.New("unavailable budget")
	p.checkSLABreachAt(ctx, m, now.Add(3*time.Minute))
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("failed query became alert: %v", events)
	}
	s.fail = nil
	for len(p.notifyChan) < cap(p.notifyChan) {
		p.notifyChan <- NotificationEvent{EventType: "fixture"}
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	p.checkSLABreachAt(deadline, m, now.Add(4*time.Minute))
	state, _ := p.slaStates.Load(m.ID)
	if !state.(slaAlertState).NotifiedAt.IsZero() {
		t.Fatal("failed queue marked alert delivered")
	}
	drainEvents(p)
	p.checkSLABreachAt(ctx, m, now.Add(5*time.Minute))
	if events := drainEvents(p); countEvents(events, "sla.breach") != 1 {
		t.Fatalf("failed queue did not retry: %v", events)
	}
}

func TestSLABatchReconcilesQuietManualMonitorAndSkipsBusyLock(t *testing.T) {
	s, p, m := slaPipelineFixture(t)
	s.up = 0
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	unlock := p.lockMonitorStatus(m.ID)
	p.reconcileSLABatch(ctx, 0, now)
	unlock()
	if s.reads != 0 {
		t.Fatal("reconciler read budget of a busy monitor")
	}
	if cursor := p.reconcileSLABatch(ctx, 0, now); cursor != 0 {
		t.Fatalf("finished batch cursor=%d", cursor)
	}
	if events := drainEvents(p); countEvents(events, "sla.breach") != 1 {
		t.Fatalf("quiet manual monitor missed threshold: %v", events)
	}
}

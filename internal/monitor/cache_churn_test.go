package monitor

import (
	"context"
	"sync"
	"testing"

	"github.com/y0f/asura/v2/internal/checker"
	"github.com/y0f/asura/v2/internal/incident"
	"github.com/y0f/asura/v2/internal/storage"
)

func assertMonitorCaches(t *testing.T, p *Pipeline, id int64, present bool) {
	t.Helper()
	for name, cache := range map[string]*sync.Map{
		"resend": &p.lastNotified, "SLA": &p.slaStates, "sources": &p.sources,
		"ordering": &p.sourceCheckedAt, "revision": &p.sourceRevision, "incident": &p.incidentOpen,
	} {
		if _, ok := cache.Load(id); ok != present {
			t.Errorf("monitor %d %s cache present=%v, want %v", id, name, ok, present)
		}
	}
}

func TestPipelineReloadReclaimsDeletedMonitorCaches(t *testing.T) {
	store := testStore(t)
	logger := discardLogger()
	p := NewPipeline(store, checker.NewRegistry(), incident.NewManager(store, logger), 1, false, logger)
	ctx := context.Background()
	for range 30 {
		mon := createTestMonitor(t, store, &storage.Monitor{SLATarget: 99})
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "down", Message: "fixture outage"}})
		status, err := store.GetMonitorStatus(ctx, mon.ID)
		if err != nil || status.ConsecFails != 1 {
			t.Fatalf("fixture check did not commit: %+v %v", status, err)
		}
		assertMonitorCaches(t, p, mon.ID, true)
		drainEvents(p)
		if err := store.DeleteMonitor(ctx, mon.ID); err != nil {
			t.Fatal(err)
		}
		p.scheduler.loadMonitors(ctx)
		assertMonitorCaches(t, p, mon.ID, false)
	}
}

func BenchmarkMonitorStatusLockSteady(b *testing.B) {
	var pipeline Pipeline
	b.ReportAllocs()
	for b.Loop() {
		pipeline.lockMonitorStatus(1)()
	}
}

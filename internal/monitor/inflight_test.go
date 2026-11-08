package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestSchedulerDoesNotQueueOverlappingCheck(t *testing.T) {
	store := testStore(t)
	m := createTestMonitor(t, store, &storage.Monitor{Interval: 1, Timeout: 10})
	jobs := make(chan Job, 4)
	s := NewScheduler(store, jobs, discardLogger())
	s.loadMonitors(context.Background())
	now := time.Now().Add(time.Second)
	s.dispatch(now)
	if len(jobs) != 1 {
		t.Fatalf("initial jobs=%d", len(jobs))
	}
	job := <-jobs // Simulate a worker still checking the target.
	if job.Monitor.ID != m.ID {
		t.Fatal("wrong monitor dispatched")
	}
	s.dispatch(now.Add(2 * time.Second))
	if len(jobs) != 0 {
		t.Fatal("a second check was queued while the first was still running")
	}
	// Configuration reload must not accidentally release outstanding work.
	if err := store.SetMonitorEnabled(context.Background(), m.ID, false); err != nil {
		t.Fatal(err)
	}
	s.loadMonitors(context.Background())
	if err := store.SetMonitorEnabled(context.Background(), m.ID, true); err != nil {
		t.Fatal(err)
	}
	s.loadMonitors(context.Background())
	s.dispatch(now.Add(3 * time.Second))
	if len(jobs) != 0 {
		t.Fatal("reload released the running check")
	}
	s.complete(m.ID)
	s.dispatch(now.Add(4 * time.Second))
	if len(jobs) != 1 {
		t.Fatal("completed check did not release the next run")
	}
}

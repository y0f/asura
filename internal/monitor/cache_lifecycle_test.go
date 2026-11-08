package monitor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/y0f/asura/v2/internal/checker"
	"github.com/y0f/asura/v2/internal/incident"
	"github.com/y0f/asura/v2/internal/storage"
)

func lockReferences(locks *monitorLocks, id int64) int {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if entry := locks.entries[id]; entry != nil {
		return entry.refs
	}
	return 0
}

func TestMonitorLocksRetainWaitersAndReleaseIdleIDs(t *testing.T) {
	var locks monitorLocks
	unlock := locks.lock(1)
	var holderOnce, waiterOnce sync.Once
	releaseHolder := func() { holderOnce.Do(unlock) }
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseWaiter := func() { waiterOnce.Do(func() { close(release) }) }
	defer func() {
		releaseHolder()
		releaseWaiter()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("waiting lock user did not finish")
		}
	}()
	go func() {
		defer close(done)
		unlock := locks.lock(1)
		close(entered)
		<-release
		unlock()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for lockReferences(&locks, 1) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("holder and waiter not retained together")
		}
		time.Sleep(time.Millisecond)
	}
	if release, ok := locks.tryLock(1); ok {
		release()
		t.Fatal("TryLock bypassed current holder")
	}
	releaseHolder()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not acquire the released lock")
	}
	if release, ok := locks.tryLock(1); ok {
		release()
		t.Fatal("new mutex replaced the one retained by a waiter")
	}
	if release, ok := locks.tryLock(2); !ok {
		t.Fatal("unrelated monitor was serialized behind a busy ID")
	} else {
		release()
	}
	releaseWaiter()
	<-done
	for id := int64(1); id <= 1000; id++ {
		locks.lock(id)()
	}
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.entries) != 0 {
		t.Fatalf("idle historical lock IDs retained: %d", len(locks.entries))
	}
}

func TestPipelineCacheCleanupPreservesDisabledAndReenabledMonitor(t *testing.T) {
	store := testStore(t)
	logger := discardLogger()
	p := NewPipeline(store, checker.NewRegistry(), incident.NewManager(store, logger), 1, false, logger)
	ctx := context.Background()
	mon := createTestMonitor(t, store, &storage.Monitor{SLATarget: 99})
	p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "down"}})
	assertMonitorCaches(t, p, mon.ID, true)
	if err := store.SetMonitorEnabled(ctx, mon.ID, false); err != nil {
		t.Fatal(err)
	}
	if !p.scheduler.loadMonitors(ctx) {
		t.Fatal("disabled existing monitor caused a retry")
	}
	assertMonitorCaches(t, p, mon.ID, true)
	if err := store.SetMonitorEnabled(ctx, mon.ID, true); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.handleResult(ctx, WorkerResult{Monitor: current, Result: &checker.Result{Status: "down"}})
	// The old scheduler snapshot does not contain the newly enabled monitor.
	if !p.pruneDeletedMonitorCaches(ctx, nil) {
		t.Fatal("current existing monitor caused a retry")
	}
	assertMonitorCaches(t, p, mon.ID, true)
	if revision, _ := p.sourceRevision.Load(mon.ID); revision != current.ConfigRevision {
		t.Fatal("stale snapshot removed new revision's source ordering")
	}
}

type cacheRetryStore struct {
	storage.Store
	mu    sync.Mutex
	reads int
}

func (s *cacheRetryStore) GetAllEnabledMonitors(context.Context) ([]*storage.Monitor, error) {
	return nil, nil
}

func (s *cacheRetryStore) MonitorMatchesAccess(context.Context, int64, ...storage.MonitorAccess) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.reads == 1 {
		return false, errors.New("temporary existence read failure")
	}
	return false, nil
}

func TestSchedulerRetriesFailedCacheCleanupWithoutHealthyQueries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &cacheRetryStore{}
		p := NewPipeline(store, checker.NewRegistry(), nil, 1, false, discardLogger())
		p.sourceRevision.Store(int64(1), int64(1))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.scheduler.Run(ctx)
		synctest.Wait()
		if _, ok := p.sourceRevision.Load(int64(1)); !ok {
			t.Fatal("read failure discarded cached ordering")
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if _, ok := p.sourceRevision.Load(int64(1)); ok {
			t.Fatal("confirmed deletion was not retried without another mutation")
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		store.mu.Lock()
		defer store.mu.Unlock()
		if store.reads != 2 {
			t.Fatalf("healthy cleanup kept querying: %d", store.reads)
		}
	})
}

type heldCacheCheckStore struct {
	*storage.SQLiteStore
	hold             atomic.Bool
	entered, release chan struct{}
}

func (s *heldCacheCheckStore) RecordMonitorCheck(ctx context.Context, mon *storage.Monitor, result *storage.CheckResult, status *storage.MonitorStatus) error {
	if s.hold.Swap(false) {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.SQLiteStore.RecordMonitorCheck(ctx, mon, result, status)
}

func TestPipelineDeletionDuringActiveAndWaitingChecks(t *testing.T) {
	store := &heldCacheCheckStore{SQLiteStore: testStore(t), entered: make(chan struct{}), release: make(chan struct{})}
	logger := discardLogger()
	p := NewPipeline(store, checker.NewRegistry(), incident.NewManager(store, logger), 1, false, logger)
	ctx := context.Background()
	mon := createTestMonitor(t, store, &storage.Monitor{SLATarget: 99})
	check := func() { p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "down"}}) }
	check()
	store.hold.Store(true)
	var running sync.WaitGroup
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(store.release) }) }
	defer func() { unblock(); running.Wait() }()
	running.Go(check)
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("check did not reach held persistence boundary")
	}
	running.Go(check)
	deadline := time.Now().Add(2 * time.Second)
	for lockReferences(&p.statusLocks, mon.ID) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("second check did not wait on existing monitor lock")
		}
		time.Sleep(time.Millisecond)
	}
	if err := store.DeleteMonitor(ctx, mon.ID); err != nil {
		t.Fatal(err)
	}
	if p.scheduler.loadMonitors(ctx) {
		t.Fatal("busy deleted monitor did not request a cleanup retry")
	}
	assertMonitorCaches(t, p, mon.ID, true)
	unblock()
	running.Wait()
	if !p.scheduler.loadMonitors(ctx) {
		t.Fatal("completed stale checks prevented cleanup")
	}
	assertMonitorCaches(t, p, mon.ID, false)
	check() // A late pre-deletion snapshot must not recreate any cache.
	assertMonitorCaches(t, p, mon.ID, false)
	if refs := lockReferences(&p.statusLocks, mon.ID); refs != 0 {
		t.Fatalf("finished deleted monitor retained %d lock users", refs)
	}
}

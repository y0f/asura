package monitor

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/y0f/asura/internal/storage"
)

// The fake clock exercises the real retry interval without wall-clock delays.
type retrySchedulerStore struct {
	storage.Store
	mu           sync.Mutex
	monitor      storage.Monitor
	proxy        storage.Proxy
	loads        int
	failLoad     bool
	failProxy    bool
	missingProxy bool
}

func (s *retrySchedulerStore) GetAllEnabledMonitors(context.Context) ([]*storage.Monitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.failLoad {
		s.failLoad = false
		return nil, errors.New("temporary monitor read failure")
	}
	m := s.monitor
	return []*storage.Monitor{&m}, nil
}

func (s *retrySchedulerStore) GetProxy(context.Context, int64) (*storage.Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failProxy {
		s.failProxy = false
		return nil, errors.New("temporary proxy read failure")
	}
	if s.missingProxy {
		return nil, sql.ErrNoRows
	}
	p := s.proxy
	return &p, nil
}

func (s *retrySchedulerStore) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}

func retryMonitorSnapshot(s *Scheduler) *storage.Monitor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m := s.monitors[1]; m != nil {
		copy := *m
		return &copy
	}
	return nil
}

func TestSchedulerRetriesFailedSnapshotWithoutAnotherMutation(t *testing.T) {
	for _, initial := range []bool{true, false} {
		for _, proxyFailure := range []bool{false, true} {
			name := "monitor"
			if proxyFailure {
				name = "proxy"
			}
			if initial {
				name += " initial"
			} else {
				name += " reload"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					store := &retrySchedulerStore{
						monitor: storage.Monitor{ID: 1, ConfigRevision: 1, Type: "http", Interval: 1, Enabled: true},
						proxy:   storage.Proxy{ID: 1, Protocol: "http", Host: "old.proxy.test", Port: 8080, Enabled: true},
					}
					if proxyFailure {
						store.monitor.ProxyID = &store.proxy.ID
					}
					fail := func() {
						store.mu.Lock()
						defer store.mu.Unlock()
						store.failLoad = !proxyFailure
						store.failProxy = proxyFailure
					}
					if initial {
						fail()
					}
					jobs := make(chan Job, 8)
					scheduler := NewScheduler(store, jobs, discardLogger())
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := make(chan struct{})
					go func() { defer close(done); scheduler.Run(ctx) }()
					synctest.Wait()
					failedLoads := 1
					wantRevision := int64(1)
					wantProxy := "http://old.proxy.test:8080"
					if !initial {
						store.mu.Lock()
						store.monitor.ConfigRevision = 2
						store.proxy.Host = "new.proxy.test"
						store.mu.Unlock()
						wantRevision, wantProxy = 2, "http://new.proxy.test:8080"
						fail()
						scheduler.TriggerReload()
						synctest.Wait()
						failedLoads++
						if mon := retryMonitorSnapshot(scheduler); mon == nil || mon.ConfigRevision != 1 {
							t.Fatal("failed reload replaced the last complete snapshot")
						}
					} else if retryMonitorSnapshot(scheduler) != nil {
						t.Fatal("failed initial load installed an incomplete snapshot")
					}
					time.Sleep(4 * time.Second)
					synctest.Wait()
					if loads := store.loadCount(); loads != failedLoads {
						t.Fatalf("retry spun before its bound: %d loads", loads)
					}
					time.Sleep(time.Second)
					synctest.Wait()
					if loads, mon := store.loadCount(), retryMonitorSnapshot(scheduler); loads != failedLoads+1 || mon == nil || mon.ConfigRevision != wantRevision {
						t.Fatalf("snapshot did not recover without a new mutation: loads=%d monitor=%v", loads, mon)
					}
					// Discard any pre-recovery queued job, then verify the next actual
					// dispatch uses the recovered revision and proxy destination.
					for len(jobs) > 0 {
						job := <-jobs
						scheduler.complete(job.Monitor.ID)
					}
					time.Sleep(time.Second)
					synctest.Wait()
					select {
					case job := <-jobs:
						if job.Monitor.ConfigRevision != wantRevision || proxyFailure && job.Monitor.ProxyURL != wantProxy {
							t.Fatalf("recovery dispatched stale configuration: %+v", job.Monitor)
						}
					default:
						t.Fatal("recovered monitor was not dispatched")
					}
					time.Sleep(15 * time.Second)
					synctest.Wait()
					if loads := store.loadCount(); loads != failedLoads+1 {
						t.Fatalf("healthy scheduler kept reloading: %d loads", loads)
					}
					cancel()
					synctest.Wait()
					select {
					case <-done:
					default:
						t.Fatal("scheduler did not stop on cancellation")
					}
				})
			})
		}
	}
}

func TestSchedulerUnavailableProxyDoesNotRetryOrBypass(t *testing.T) {
	for _, missing := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			id := int64(1)
			store := &retrySchedulerStore{missingProxy: missing,
				monitor: storage.Monitor{ID: 1, Type: "http", Interval: 1, ProxyID: &id},
				proxy:   storage.Proxy{ID: id, Enabled: false}}
			scheduler := NewScheduler(store, make(chan Job, 1), discardLogger())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go scheduler.Run(ctx)
			time.Sleep(16 * time.Second)
			synctest.Wait()
			mon := retryMonitorSnapshot(scheduler)
			if loads := store.loadCount(); loads != 1 || mon == nil || mon.ProxyID == nil || mon.ProxyURL != "" {
				t.Fatalf("unavailable route retried or lost fail-closed binding: loads=%d monitor=%+v", loads, mon)
			}
		})
	}
}

func TestSchedulerCancelsPendingRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &retrySchedulerStore{failLoad: true}
		scheduler := NewScheduler(store, make(chan Job), discardLogger())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); scheduler.Run(ctx) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("pending retry delayed cancellation")
		}
		time.Sleep(10 * time.Second)
		if loads := store.loadCount(); loads != 1 {
			t.Fatalf("retry queried storage after shutdown: %d loads", loads)
		}
	})
}

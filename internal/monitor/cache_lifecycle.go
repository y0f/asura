package monitor

import (
	"context"
	"sync"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

type monitorLock struct {
	mu   sync.Mutex
	refs int // Includes holders and callers waiting to acquire mu.
}

// monitorLocks keeps one stable mutex per in-flight monitor without retaining
// every historical monitor ID. Registry operations never wait for a monitor
// mutex while holding the registry mutex.
type monitorLocks struct {
	mu      sync.Mutex
	entries map[int64]*monitorLock
}

func (locks *monitorLocks) retain(id int64) *monitorLock {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if locks.entries == nil {
		locks.entries = make(map[int64]*monitorLock)
	}
	entry := locks.entries[id]
	if entry == nil {
		entry = &monitorLock{}
		locks.entries[id] = entry
	}
	entry.refs++
	return entry
}

func (locks *monitorLocks) release(id int64, entry *monitorLock) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(locks.entries, id)
	}
}

func (locks *monitorLocks) lock(id int64) func() {
	entry := locks.retain(id)
	entry.mu.Lock()
	return func() { entry.mu.Unlock(); locks.release(id, entry) }
}

func (locks *monitorLocks) tryLock(id int64) (func(), bool) {
	entry := locks.retain(id)
	if !entry.mu.TryLock() {
		locks.release(id, entry)
		return nil, false
	}
	return func() { entry.mu.Unlock(); locks.release(id, entry) }, true
}

func (p *Pipeline) runtimeCaches() [6]*sync.Map {
	return [6]*sync.Map{&p.lastNotified, &p.slaStates, &p.sources, &p.sourceCheckedAt, &p.sourceRevision, &p.incidentOpen}
}

// pruneDeletedMonitorCaches runs after a scheduler snapshot, outside its mutex.
// The snapshot narrows candidates, but a current scalar existence check under
// the monitor lock decides deletion. A concurrent re-enable or import cannot
// lose live ordering/streak state because it was absent in an older snapshot.
// Busy entries and transient failures use the scheduler's pending-load retry.
func (p *Pipeline) pruneDeletedMonitorCaches(ctx context.Context, enabled map[int64]struct{}) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	caches := p.runtimeCaches()
	candidates := make(map[int64]struct{})
	for _, cache := range caches {
		cache.Range(func(key, _ any) bool {
			if ctx.Err() != nil {
				return false
			}
			id := key.(int64)
			if _, active := enabled[id]; !active {
				candidates[id] = struct{}{}
			}
			return true
		})
	}
	complete := true
	for id := range candidates {
		if ctx.Err() != nil {
			return false
		}
		unlock, locked := p.statusLocks.tryLock(id)
		if !locked {
			complete = false
			continue
		}
		exists, err := p.store.MonitorMatchesAccess(ctx, id, storage.MonitorAccess{All: true})
		if err == nil && !exists {
			for _, cache := range caches {
				cache.Delete(id)
			}
		}
		unlock()
		if err != nil {
			complete = false
			if ctx.Err() == nil {
				p.logger.Error("check deleted monitor cache", "monitor_id", id, "error", err)
			}
		}
	}
	return complete && ctx.Err() == nil
}

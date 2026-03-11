package monitor

import (
	"container/heap"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type schedulerEntry struct {
	monitorID int64
	nextRun   int64 // UnixNano for fast comparison
	index     int
}

type schedulerHeap []*schedulerEntry

func (h schedulerHeap) Len() int           { return len(h) }
func (h schedulerHeap) Less(i, j int) bool { return h[i].nextRun < h[j].nextRun }
func (h schedulerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *schedulerHeap) Push(x any) {
	entry := x.(*schedulerEntry)
	entry.index = len(*h)
	*h = append(*h, entry)
}

func (h *schedulerHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	entry.index = -1
	*h = old[:n-1]
	return entry
}

// Scheduler dispatches check jobs using a min-heap ordered by next-run time.
type Scheduler struct {
	store             storage.Store
	jobs              chan<- Job
	logger            *slog.Logger
	mu                sync.RWMutex
	monitors          map[int64]*storage.Monitor
	entries           map[int64]*schedulerEntry
	heap              schedulerHeap
	effectiveInterval map[int64]int64 // nanoseconds
	reload            chan struct{}
	droppedJobs       atomic.Int64
}

func NewScheduler(store storage.Store, jobs chan<- Job, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		store:             store,
		jobs:              jobs,
		logger:            logger,
		monitors:          make(map[int64]*storage.Monitor),
		entries:           make(map[int64]*schedulerEntry),
		effectiveInterval: make(map[int64]int64),
		reload:            make(chan struct{}, 1),
	}
}

// TriggerReload signals the scheduler to reload monitors.
func (s *Scheduler) TriggerReload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	s.loadMonitors(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.reload:
			s.loadMonitors(ctx)
		case now := <-ticker.C:
			s.dispatch(now)
		}
	}
}

func (s *Scheduler) loadMonitors(ctx context.Context) {
	monitors, err := s.store.GetAllEnabledMonitors(ctx)
	if err != nil {
		s.logger.Error("scheduler: load monitors", "error", err)
		return
	}

	s.resolveProxyURLs(ctx, monitors)

	s.mu.Lock()
	defer s.mu.Unlock()

	nowNano := time.Now().UnixNano()
	activeIDs := make(map[int64]struct{}, len(monitors))
	newMonitors := make(map[int64]*storage.Monitor, len(monitors))

	for _, m := range monitors {
		activeIDs[m.ID] = struct{}{}
		newMonitors[m.ID] = m

		if _, exists := s.entries[m.ID]; !exists {
			baseNano := int64(m.Interval) * int64(time.Second)
			if _, hasEff := s.effectiveInterval[m.ID]; !hasEff {
				s.effectiveInterval[m.ID] = baseNano
			}
			entry := &schedulerEntry{monitorID: m.ID, nextRun: nowNano}
			s.entries[m.ID] = entry
			heap.Push(&s.heap, entry)
		}
	}

	for id, entry := range s.entries {
		if _, active := activeIDs[id]; !active {
			heap.Remove(&s.heap, entry.index)
			delete(s.entries, id)
			delete(s.effectiveInterval, id)
		}
	}

	s.monitors = newMonitors
	s.logger.Debug("scheduler: loaded monitors", "count", len(monitors))
}

// interval returns the effective interval in nanoseconds for a monitor,
// falling back to the monitor's base interval.
func (s *Scheduler) interval(monitorID int64, baseIntervalSecs int) int64 {
	if eff := s.effectiveInterval[monitorID]; eff > 0 {
		return eff
	}
	return int64(baseIntervalSecs) * int64(time.Second)
}

func (s *Scheduler) dispatch(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nowNano := now.UnixNano()

	for s.heap.Len() > 0 && s.heap[0].nextRun <= nowNano {
		entry := heap.Pop(&s.heap).(*schedulerEntry)

		mon, exists := s.monitors[entry.monitorID]
		if !exists {
			delete(s.entries, entry.monitorID)
			delete(s.effectiveInterval, entry.monitorID)
			continue
		}

		iv := s.interval(entry.monitorID, mon.Interval)

		if mon.Type == "heartbeat" || mon.Type == "manual" {
			entry.nextRun = nowNano + iv
			heap.Push(&s.heap, entry)
			continue
		}

		select {
		case s.jobs <- Job{Monitor: mon}:
			entry.nextRun = nowNano + iv
		default:
			s.droppedJobs.Add(1)
			s.logger.Warn("scheduler: job channel full, skipping", "monitor_id", entry.monitorID)
			entry.nextRun = nowNano + iv
		}

		heap.Push(&s.heap, entry)
	}
}

// GetMultiplier returns the current effective interval multiplier for a monitor.
func (s *Scheduler) GetMultiplier(monitorID int64) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	eff, ok := s.effectiveInterval[monitorID]
	if !ok {
		return 1.0
	}

	mon, exists := s.monitors[monitorID]
	if !exists || mon.Interval <= 0 {
		return 1.0
	}

	base := int64(mon.Interval) * int64(time.Second)
	return float64(eff) / float64(base)
}

// resolveProxyURLs populates ProxyURL for monitors that have a ProxyID set.
func (s *Scheduler) resolveProxyURLs(ctx context.Context, monitors []*storage.Monitor) {
	proxyIDs := make(map[int64]struct{})
	for _, m := range monitors {
		if m.ProxyID != nil {
			proxyIDs[*m.ProxyID] = struct{}{}
		}
	}
	if len(proxyIDs) == 0 {
		return
	}

	proxyCache := make(map[int64]string)
	for id := range proxyIDs {
		p, err := s.store.GetProxy(ctx, id)
		if err != nil || !p.Enabled {
			continue
		}
		u := &url.URL{
			Scheme: p.Protocol,
			Host:   fmt.Sprintf("%s:%d", p.Host, p.Port),
		}
		if p.AuthUser != "" {
			u.User = url.UserPassword(p.AuthUser, p.AuthPass)
		}
		proxyCache[id] = u.String()
	}

	for _, m := range monitors {
		if m.ProxyID != nil {
			m.ProxyURL = proxyCache[*m.ProxyID]
		}
	}
}

// UpdateInterval sets the effective interval for a monitor and adjusts the heap.
func (s *Scheduler) UpdateInterval(monitorID int64, interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nano := int64(interval)
	s.effectiveInterval[monitorID] = nano

	entry, exists := s.entries[monitorID]
	if !exists {
		return
	}

	mon, monExists := s.monitors[monitorID]
	if !monExists {
		return
	}

	base := int64(mon.Interval) * int64(time.Second)
	if base != nano {
		heap.Fix(&s.heap, entry.index)
	}
}

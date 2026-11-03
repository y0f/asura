package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

// HeartbeatWatcher periodically checks for expired heartbeats and triggers incidents.
type HeartbeatWatcher struct {
	store    storage.Store
	incMgr   *incident.Manager
	pipeline *Pipeline
	interval time.Duration
	logger   *slog.Logger
}

// NewHeartbeatWatcher creates a watcher that scans for expired heartbeats.
func NewHeartbeatWatcher(store storage.Store, incMgr *incident.Manager, pipeline *Pipeline, interval time.Duration, logger *slog.Logger) *HeartbeatWatcher {
	return &HeartbeatWatcher{
		store:    store,
		incMgr:   incMgr,
		pipeline: pipeline,
		interval: interval,
		logger:   logger,
	}
}

// Run starts the heartbeat watcher loop.
func (w *HeartbeatWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check(ctx)
		}
	}
}

func (w *HeartbeatWatcher) check(ctx context.Context) {
	expired, err := w.store.ListExpiredHeartbeats(ctx)
	if err != nil {
		w.logger.Error("heartbeat watcher: list expired", "error", err)
		return
	}

	for _, hb := range expired {
		if hb.Status == "down" {
			w.resendIfNeeded(ctx, hb)
			continue
		}

		// Re-read the latest committed heartbeat snapshot to avoid racing a ping
		// that arrived between the expired-listing SELECT and this transition. In
		// WAL mode committed pings are visible to the read pool, so if last_ping_at
		// advanced the monitor recovered and we must not mark it down.
		fresh, err := w.store.GetHeartbeatByMonitorID(ctx, hb.MonitorID)
		if err != nil {
			w.logger.Error("heartbeat watcher: re-fetch heartbeat", "error", err)
			continue
		}
		if fresh.Status == "down" || pingAdvanced(hb.LastPingAt, fresh.LastPingAt) {
			continue
		}

		w.logger.Info("heartbeat expired", "monitor_id", hb.MonitorID)

		// Mark heartbeat as down
		if err := w.store.UpdateHeartbeatStatus(ctx, hb.MonitorID, "down"); err != nil {
			w.logger.Error("heartbeat watcher: mark down", "monitor_id", hb.MonitorID, "error", err)
			continue
		}

		// Get monitor for incident creation
		mon, err := w.store.GetMonitor(ctx, hb.MonitorID)
		if err != nil {
			w.logger.Error("heartbeat watcher: get monitor", "error", err)
			continue
		}

		// Update monitor status to down
		now := time.Now()
		status := &storage.MonitorStatus{
			MonitorID:   mon.ID,
			Status:      "down",
			LastCheckAt: &now,
			ConsecFails: mon.FailureThreshold,
		}
		if err := w.store.UpsertMonitorStatus(ctx, status); err != nil {
			w.logger.Error("heartbeat watcher: upsert status", "error", err)
		}

		// Create the incident, notify and start escalation exactly like the
		// check pipeline does.
		w.pipeline.processFailure(ctx, mon, "down", "heartbeat missed", w.pipeline.inMaintenance(ctx, mon.ID))
	}
}

// pingAdvanced reports whether a heartbeat ping arrived after the one observed
// when the heartbeat was listed as expired.
func pingAdvanced(observed, current *time.Time) bool {
	if current == nil {
		return false
	}
	if observed == nil {
		return true
	}
	return current.After(*observed)
}

func (w *HeartbeatWatcher) resendIfNeeded(ctx context.Context, hb *storage.Heartbeat) {
	mon, err := w.store.GetMonitor(ctx, hb.MonitorID)
	if err != nil {
		return
	}
	if _, silenced := w.pipeline.silenced.Load(mon.ID); silenced {
		w.announceAfterMaintenance(ctx, mon)
		return
	}
	if mon.ResendInterval <= 0 {
		return
	}
	if !w.pipeline.shouldResend(mon) {
		return
	}
	inMaintenance, _ := w.store.IsMonitorInMaintenance(ctx, mon.ID, time.Now())
	if inMaintenance {
		return
	}
	inc, err := w.store.GetOpenIncident(ctx, mon.ID)
	if err != nil || inc == nil {
		return
	}
	w.pipeline.emitNotification(ctx, "incident.reminder", inc, mon, nil)
	w.pipeline.lastNotified.Store(mon.ID, time.Now())
}

// announceAfterMaintenance announces an incident that was opened silently
// during a maintenance window once that window has ended.
func (w *HeartbeatWatcher) announceAfterMaintenance(ctx context.Context, mon *storage.Monitor) {
	if w.pipeline.inMaintenance(ctx, mon.ID) {
		return
	}
	inc, err := w.store.GetOpenIncident(ctx, mon.ID)
	if err != nil || inc == nil {
		w.pipeline.silenced.Delete(mon.ID)
		return
	}
	if w.pipeline.takeSilenced(mon.ID, inc.ID) {
		w.pipeline.announceIncident(ctx, mon, inc)
	}
}

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

		w.logger.Info("heartbeat expired", "monitor_id", hb.MonitorID)

		// Mark heartbeat as down
		w.store.UpdateHeartbeatStatus(ctx, hb.MonitorID, "down")

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

		// Create incident through the incident manager
		inMaintenance, _ := w.store.IsMonitorInMaintenance(ctx, mon.ID, now)
		inc, created, err := w.incMgr.ProcessFailure(ctx, mon.ID, mon.Name, "down", "heartbeat missed")
		if err != nil {
			w.logger.Error("heartbeat watcher: process failure", "error", err)
			continue
		}
		if created && !inMaintenance {
			w.pipeline.emitNotification("incident.created", inc, mon, nil)
			w.pipeline.lastNotified.Store(mon.ID, time.Now())
		}
	}
}

func (w *HeartbeatWatcher) resendIfNeeded(ctx context.Context, hb *storage.Heartbeat) {
	mon, err := w.store.GetMonitor(ctx, hb.MonitorID)
	if err != nil || mon.ResendInterval <= 0 {
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
	w.pipeline.emitNotification("incident.reminder", inc, mon, nil)
	w.pipeline.lastNotified.Store(mon.ID, time.Now())
}

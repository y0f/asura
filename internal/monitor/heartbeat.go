package monitor

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/y0f/asura/v3/internal/incident"
	"github.com/y0f/asura/v3/internal/storage"
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
		w.checkHeartbeat(ctx, hb.MonitorID)
	}
}

func (w *HeartbeatWatcher) checkHeartbeat(ctx context.Context, monitorID int64) {
	unlock := w.pipeline.lockMonitorStatus(monitorID)
	defer unlock()

	// The scan is only a candidate list. Both reminders and expiry use fresh
	// state under the same lock as pings, including their incident transitions.
	hb, err := w.store.GetHeartbeatByMonitorID(ctx, monitorID)
	if err != nil {
		w.logger.Error("heartbeat watcher: get heartbeat", "error", err)
		return
	}
	if hb.Status == "down" {
		w.resendIfNeeded(ctx, hb)
		return
	}
	mon, err := w.store.GetMonitor(ctx, monitorID)
	if err != nil {
		w.logger.Error("heartbeat watcher: get monitor", "error", err)
		return
	}
	expired, err := w.store.ExpireHeartbeat(ctx, monitorID)
	if err != nil {
		w.logger.Error("heartbeat watcher: mark down", "monitor_id", monitorID, "error", err)
		return
	}
	if !expired {
		return
	}
	w.logger.Info("heartbeat expired", "monitor_id", monitorID)
	w.pipeline.processFailure(ctx, mon, "down", "heartbeat missed", w.pipeline.inMaintenance(ctx, monitorID))
}

func (w *HeartbeatWatcher) resendIfNeeded(ctx context.Context, hb *storage.Heartbeat) {
	mon, err := w.store.GetMonitor(ctx, hb.MonitorID)
	if err != nil || !mon.Enabled || mon.Type != "heartbeat" {
		return
	}
	// Expiry may have committed just before incident creation failed or the
	// process stopped. Retry that transition even when reminders are disabled.
	inc, err := w.store.GetOpenIncident(ctx, mon.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && inc == nil) {
		w.pipeline.processFailure(ctx, mon, "down", "heartbeat missed", w.pipeline.inMaintenance(ctx, mon.ID))
		return
	}
	if err != nil {
		w.logger.Error("heartbeat watcher: get open incident", "monitor_id", mon.ID, "error", err)
		return
	}
	w.pipeline.checkSLABreach(ctx, mon)
	if inc.AnnouncementPending {
		if !w.pipeline.inMaintenance(ctx, mon.ID) {
			w.pipeline.announceIncident(ctx, mon, inc)
		}
		return
	}
	resend := w.pipeline.shouldResend(mon)
	if !inc.EscalationPending && !resend {
		return
	}
	inMaintenance, _ := w.store.IsMonitorInMaintenance(ctx, mon.ID, time.Now())
	if inMaintenance {
		return
	}
	w.pipeline.prepareEscalation(ctx, mon, inc)
	if resend {
		w.pipeline.emitNotification(ctx, "incident.reminder", inc, mon, nil)
		w.pipeline.lastNotified.Store(mon.ID, time.Now())
	}
}

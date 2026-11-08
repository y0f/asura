package monitor

import (
	"context"
	"time"
)

// Manual monitors have no scheduled check to retry a deferred announcement.
// Reconcile durable pending incidents on startup and in small, indexed batches.
func (p *Pipeline) reconcilePendingAnnouncements(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	var cursor int64
	for {
		passCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		cursor = p.reconcilePendingBatch(passCtx, cursor)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Pipeline) reconcilePendingBatch(ctx context.Context, cursor int64) int64 {
	const batchSize = 100
	ids, err := p.store.ListPendingIncidentIDs(ctx, cursor, batchSize)
	if err != nil {
		if ctx.Err() == nil {
			p.logger.Error("list pending incident announcements", "error", err)
		}
		return cursor
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return cursor
		}
		p.reconcilePendingIncident(ctx, id)
		cursor = id
	}
	if len(ids) < batchSize {
		return 0
	}
	return cursor
}

func (p *Pipeline) reconcilePendingIncident(ctx context.Context, id int64) {
	inc, err := p.store.GetIncident(ctx, id)
	if err != nil {
		return
	}
	unlock, locked := p.statusLocks.tryLock(inc.MonitorID)
	if !locked {
		return // Retry a busy transition in a later batch without delaying others.
	}
	defer unlock()
	// Recheck after taking the status lock: a ping or check may have resolved it
	// while this batch was waiting for a previous transition.
	inc, err = p.store.GetIncident(ctx, id)
	if err != nil || inc.Status == "resolved" {
		return
	}
	mon, err := p.store.GetMonitor(ctx, inc.MonitorID)
	if err != nil || !mon.Enabled || (mon.Status != "down" && mon.Status != "degraded") {
		return
	}
	if p.inMaintenance(ctx, mon.ID) {
		return
	}
	if inc.AnnouncementPending {
		p.announceIncident(ctx, mon, inc)
	} else {
		p.prepareEscalation(ctx, mon, inc)
	}
}

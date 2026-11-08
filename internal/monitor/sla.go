package monitor

import (
	"context"
	"time"

	"github.com/y0f/asura/v2/internal/sla"
	"github.com/y0f/asura/v2/internal/storage"
)

type slaAlertState struct {
	Period     string
	Target     float64
	CheckedAt  time.Time
	NotifiedAt time.Time
	Level      int
}

// Callers hold the monitor status lock. Evaluate at most once a minute during
// an ongoing outage; higher severity and a new month bypass hourly alert dedupe.
func (p *Pipeline) checkSLABreach(ctx context.Context, mon *storage.Monitor) {
	p.checkSLABreachAt(ctx, mon, time.Now().UTC())
}

func (p *Pipeline) checkSLABreachAt(ctx context.Context, mon *storage.Monitor, now time.Time) {
	if !mon.Enabled || !mon.HasSampledMetrics() || mon.SLATarget <= 0 {
		p.slaStates.Delete(mon.ID)
		return
	}
	period := now.UTC().Format("2006-01")
	state := slaAlertState{Period: period, Target: mon.SLATarget}
	if cached, ok := p.slaStates.Load(mon.ID); ok {
		previous := cached.(slaAlertState)
		if previous.Period == period && previous.Target == mon.SLATarget {
			state = previous
			if elapsed := now.Sub(state.CheckedAt); elapsed >= 0 && elapsed < time.Minute {
				return
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	state.CheckedAt = now
	defer func() { p.slaStates.Store(mon.ID, state) }()
	inMaintenance, err := p.store.IsMonitorInMaintenance(ctx, mon.ID, now)
	if err != nil || inMaintenance {
		return // An unreadable suppression state must not produce an alert.
	}
	status, err := sla.ComputeMonthly(ctx, p.store, mon.ID, now)
	if err != nil {
		p.logger.Warn("evaluate SLA budget", "monitor_id", mon.ID, "error", err)
		return
	}
	if status.TotalChecks == 0 {
		return
	}
	level := 0
	if status.Breached {
		level = 2
	} else if status.BudgetAtRisk {
		level = 1
	}
	sinceNotification := now.Sub(state.NotifiedAt)
	if level == 0 || (level <= state.Level && sinceNotification >= 0 && sinceNotification < time.Hour) {
		return
	}
	if p.emitNotification(ctx, "sla.breach", nil, mon, nil) {
		state.Level, state.NotifiedAt = level, now
	}
}

// A prolonged active-check outage may have no fresh result. Scan
// in keyset batches with a two-second budget, continuing from the cursor next
// pass. Busy monitors are skipped without delaying other monitors' transitions.
func (p *Pipeline) reconcileSLABudgets(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var cursor int64
	for {
		passCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		for {
			previous := cursor
			cursor = p.reconcileSLABatch(passCtx, cursor, time.Now().UTC())
			if cursor == 0 || cursor == previous || passCtx.Err() != nil {
				break
			}
		}
		cancel()
		// Discard long-idle entries left by deleted/disabled SLA monitors.
		p.slaStates.Range(func(id, value any) bool {
			if time.Since(value.(slaAlertState).CheckedAt) > 48*time.Hour {
				p.slaStates.CompareAndDelete(id, value)
			}
			return true
		})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Pipeline) reconcileSLABatch(ctx context.Context, cursor int64, now time.Time) int64 {
	const batchSize = 100
	ids, err := p.store.ListSLAMonitorIDs(ctx, cursor, batchSize)
	if err != nil {
		return cursor
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return cursor
		}
		unlock, locked := p.statusLocks.tryLock(id)
		if locked {
			mon, err := p.store.GetMonitor(ctx, id)
			if err == nil {
				p.checkSLABreachAt(ctx, mon, now)
			}
			unlock()
		}
		cursor = id
	}
	if len(ids) < batchSize {
		return 0
	}
	return cursor
}

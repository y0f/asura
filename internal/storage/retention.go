package storage

import (
	"context"
	"log/slog"
	"time"
)

// RetentionWorker periodically purges old data and computes rollups.
type RetentionWorker struct {
	store                   Store
	retentionDays           int
	requestLogRetentionDays int
	period                  time.Duration
	logger                  *slog.Logger
}

func NewRetentionWorker(store Store, retentionDays, requestLogRetentionDays int, period time.Duration, logger *slog.Logger) *RetentionWorker {
	return &RetentionWorker{
		store:                   store,
		retentionDays:           retentionDays,
		requestLogRetentionDays: requestLogRetentionDays,
		period:                  period,
		logger:                  logger,
	}
}

func (w *RetentionWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.period)
	defer ticker.Stop()

	// Roll up before purging so the oldest hours are summarized while their
	// raw rows still exist.
	w.rollup(ctx)
	w.purge(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.rollup(ctx)
			w.purge(ctx)
		}
	}
}

func (w *RetentionWorker) purge(ctx context.Context) {
	before := time.Now().AddDate(0, 0, -w.retentionDays)
	deleted, err := w.store.PurgeOldData(ctx, before)
	if err != nil {
		w.logger.Error("retention purge failed", "error", err)
		return
	}
	if deleted > 0 {
		w.logger.Info("retention purge completed", "deleted", deleted, "before", before.Format(time.RFC3339))
	}

	rlBefore := time.Now().AddDate(0, 0, -w.requestLogRetentionDays)
	rlDeleted, err := w.store.PurgeOldRequestLogs(ctx, rlBefore)
	if err != nil {
		w.logger.Error("request log purge failed", "error", err)
		return
	}
	if rlDeleted > 0 {
		w.logger.Info("request log purge completed", "deleted", rlDeleted, "before", rlBefore.Format(time.RFC3339))
	}

	hourlyBefore := time.Now().AddDate(0, 0, -90)
	if d, err := w.store.PurgeHourlyBefore(ctx, hourlyBefore); err != nil {
		w.logger.Error("hourly rollup purge failed", "error", err)
	} else if d > 0 {
		w.logger.Info("hourly rollup purge completed", "deleted", d)
	}

	dailyBefore := time.Now().AddDate(-1, 0, 0)
	if d, err := w.store.PurgeDailyBefore(ctx, dailyBefore); err != nil {
		w.logger.Error("daily rollup purge failed", "error", err)
	} else if d > 0 {
		w.logger.Info("daily rollup purge completed", "deleted", d)
	}
}

// rollupProgress is implemented by stores that can report how far the rollups
// have got, so the worker can catch up on hours and days missed while it was
// not running. Zero times mean "none".
type rollupProgress interface {
	RollupProgress(ctx context.Context) (lastHour, lastDay, oldestCheck time.Time, err error)
}

func (w *RetentionWorker) rollup(ctx context.Context) {
	now := time.Now().UTC()
	curHour := now.Truncate(time.Hour)
	today := now.Truncate(24 * time.Hour)

	// By default re-roll the last 24 hours and 2 days so late results are
	// included; reach further back if earlier periods were never rolled up.
	firstHour := curHour.Add(-24 * time.Hour)
	firstDay := today.AddDate(0, 0, -2)
	if rp, ok := w.store.(rollupProgress); ok {
		lastHour, lastDay, oldest, err := rp.RollupProgress(ctx)
		if err != nil {
			w.logger.Error("rollup progress lookup failed", "error", err)
		} else {
			from := lastHour
			if from.IsZero() {
				from = oldest.Truncate(time.Hour)
			}
			if !from.IsZero() && from.Before(firstHour) {
				firstHour = from
			}
			dayFrom := lastDay
			if dayFrom.IsZero() {
				dayFrom = firstHour.Truncate(24 * time.Hour)
			}
			if dayFrom.Before(firstDay) {
				firstDay = dayFrom
			}
		}
	}

	// Never recompute an hour that retention has already (partly) purged: the
	// stored rollup would be replaced by partial counts.
	if w.retentionDays > 0 {
		cutoff := now.AddDate(0, 0, -w.retentionDays)
		if firstHour.Before(cutoff) {
			firstHour = cutoff.Truncate(time.Hour)
			if firstHour.Before(cutoff) {
				firstHour = firstHour.Add(time.Hour)
			}
		}
	}

	for t := firstHour; t.Before(curHour); t = t.Add(time.Hour) {
		hour := t.Format("2006-01-02T15")
		if err := w.store.RollupHourly(ctx, hour); err != nil {
			w.logger.Error("hourly rollup failed", "hour", hour, "error", err)
		}
	}

	// Daily rollups are built from the hourly table, which outlives the raw rows.
	for t := firstDay; t.Before(today); t = t.AddDate(0, 0, 1) {
		day := t.Format("2006-01-02")
		if err := w.store.RollupDaily(ctx, day); err != nil {
			w.logger.Error("daily rollup failed", "day", day, "error", err)
		}
	}
}

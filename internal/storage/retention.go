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

	w.purge(ctx)
	w.rollup(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.purge(ctx)
			w.rollup(ctx)
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

func (w *RetentionWorker) rollup(ctx context.Context) {
	now := time.Now().UTC()

	for h := 1; h <= 24; h++ {
		t := now.Add(-time.Duration(h) * time.Hour)
		hour := t.Format("2006-01-02T15")
		if err := w.store.RollupHourly(ctx, hour); err != nil {
			w.logger.Error("hourly rollup failed", "hour", hour, "error", err)
		}
	}

	for d := 1; d <= 2; d++ {
		t := now.AddDate(0, 0, -d)
		day := t.Format("2006-01-02")
		if err := w.store.RollupDaily(ctx, day); err != nil {
			w.logger.Error("daily rollup failed", "day", day, "error", err)
		}
	}
}

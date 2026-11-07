package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// RollupPending summarizes closed periods before retention can delete their
// sources. The checkpoint advances only after every hourly and daily write
// succeeds. IDs find late additions and holes; the time range includes rows
// seen previously while their hour was still open. Normal runs therefore read
// only new results and newly closed hours, rather than the whole retention span.
func (s *SQLiteStore) RollupPending(ctx context.Context, now time.Time) error {
	s.rollupMu.Lock()
	defer s.rollupMu.Unlock()
	if err := s.initializeRollupFloors(ctx); err != nil {
		return err
	}
	end := now.UTC().Truncate(time.Hour)
	var lastID, maxID int64
	var previous string
	if err := s.readDB.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT CAST(value AS INTEGER) FROM meta WHERE key='rollup_scan_id'), 0),
		COALESCE((SELECT value FROM meta WHERE key='rollup_before'), ''),
		COALESCE((SELECT MAX(id) FROM check_results), 0)`).Scan(&lastID, &previous, &maxID); err != nil {
		return err
	}
	start := previous
	if start == "" {
		start = formatTime(end)
	}
	hours, err := s.rollupPeriods(ctx, `SELECT substr(created_at, 1, 13) FROM check_results
		WHERE id > ? AND id <= ? AND created_at < ?
		UNION SELECT substr(created_at, 1, 13) FROM check_results
		WHERE created_at >= ? AND created_at < ?`, lastID, maxID, formatTime(end), start, formatTime(end))
	if err != nil {
		return err
	}
	daySet := make(map[string]bool)
	for _, hour := range hours {
		if err := s.RollupHourly(ctx, hour); err != nil {
			return fmt.Errorf("roll up hour %s: %w", hour, err)
		}
		daySet[hour[:10]] = true
	}
	// Include existing hourly history on the first run and the day that just
	// closed thereafter, even when no new raw result arrived at midnight.
	dayStart := ""
	if previous != "" {
		dayStart = previous[:10] + "T00"
	}
	today := end.Format("2006-01-02")
	days, err := s.rollupPeriods(ctx, `SELECT DISTINCT substr(hour, 1, 10)
		FROM check_result_hourly WHERE hour >= ? AND hour < ?`, dayStart, today+"T00")
	if err != nil {
		return err
	}
	for _, day := range days {
		daySet[day] = true
	}
	days = days[:0]
	for day := range daySet {
		if day < today {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	for _, day := range days {
		if err := s.RollupDaily(ctx, day); err != nil {
			return fmt.Errorf("roll up day %s: %w", day, err)
		}
	}
	// One statement commits both progress markers. A failed or interrupted
	// pass safely repeats completed buckets instead of skipping unfinished ones.
	_, err = s.writeDB.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES
		('rollup_scan_id', ?), ('rollup_before', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, max(lastID, maxID), formatTime(end))
	return err
}

func (s *SQLiteStore) rollupPeriods(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var periods []string
	for rows.Next() {
		var period string
		if err := rows.Scan(&period); err != nil {
			return nil, err
		}
		periods = append(periods, period)
	}
	return periods, rows.Err()
}

// Older versions did not record their purge boundaries. Conservatively protect
// existing summaries at the oldest remaining source bucket, which might be
// partial. Missing summaries can still be created from the surviving source.
// This initialization is recorded even on a fresh database (with empty floors).
func (s *SQLiteStore) initializeRollupFloors(ctx context.Context) error {
	var initialized int
	if err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM meta
		WHERE key IN ('raw_purged_before', 'hourly_purged_before')`).Scan(&initialized); err != nil {
		return err
	}
	if initialized == 2 {
		return nil
	}
	var firstRaw, firstHour, lastHour, lastDay sql.NullString
	if err := s.readDB.QueryRowContext(ctx, `SELECT
		(SELECT MIN(created_at) FROM check_results),
		(SELECT MIN(hour) FROM check_result_hourly),
		(SELECT MAX(hour) FROM check_result_hourly),
		(SELECT MAX(day) FROM check_result_daily)`).Scan(&firstRaw, &firstHour, &lastHour, &lastDay); err != nil {
		return err
	}
	ceil := func(t time.Time, unit time.Duration) time.Time {
		floor := t.Truncate(unit)
		if floor.Before(t) {
			return floor.Add(unit)
		}
		return floor
	}
	rawFloor, hourlyFloor := "", ""
	if lastHour.Valid {
		if firstRaw.Valid {
			rawFloor = formatTime(ceil(parseTime(firstRaw.String), time.Hour))
		} else {
			t, err := time.Parse("2006-01-02T15", lastHour.String)
			if err != nil {
				return err
			}
			rawFloor = formatTime(t.Add(time.Hour))
		}
	}
	if lastDay.Valid {
		if firstHour.Valid {
			t, err := time.Parse("2006-01-02T15", firstHour.String)
			if err != nil {
				return err
			}
			hourlyFloor = ceil(t, 24*time.Hour).Format("2006-01-02T15")
		} else {
			t, err := time.Parse("2006-01-02", lastDay.String)
			if err != nil {
				return err
			}
			hourlyFloor = t.Add(24 * time.Hour).Format("2006-01-02T15")
		}
	}
	_, err := s.writeDB.ExecContext(ctx, `INSERT OR IGNORE INTO meta (key, value)
		VALUES ('raw_purged_before', ?), ('hourly_purged_before', ?)`, rawFloor, hourlyFloor)
	return err
}

func (s *SQLiteStore) advancePurgeFloor(ctx context.Context, key, before string) error {
	_, err := s.writeDB.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=MAX(meta.value, excluded.value)`, key, before)
	return err
}

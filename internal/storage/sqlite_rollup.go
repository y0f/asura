package storage

import (
	"context"
	"fmt"
	"time"
)

const rollupReplacement = `sum_rt=excluded.sum_rt, avg_rt=excluded.avg_rt, min_rt=excluded.min_rt,
	max_rt=excluded.max_rt, p95_rt=excluded.p95_rt, up_count=excluded.up_count,
	down_count=excluded.down_count, total=excluded.total`

func (s *SQLiteStore) RollupHourly(ctx context.Context, hour string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO check_result_hourly (monitor_id, hour, sum_rt, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total)
		 WITH windowed AS (
		   SELECT monitor_id, response_time,
		          ROW_NUMBER() OVER (PARTITION BY monitor_id ORDER BY response_time) AS rn,
		          COUNT(*)     OVER (PARTITION BY monitor_id)                         AS n
		   FROM check_results
		   WHERE created_at >= ? AND created_at < ?
		 ),
		 p95 AS (
		   SELECT monitor_id, COALESCE(MIN(CASE WHEN rn >= n * 0.95 THEN response_time END), 0) AS p95_rt
		   FROM windowed GROUP BY monitor_id
		 ),
		 agg AS (
		   SELECT monitor_id,
		          SUM(response_time) AS sum_rt,
		          `+s.writeDB.dialect.integer("AVG(response_time)")+` AS avg_rt,
		          MIN(response_time) AS min_rt,
		          MAX(response_time) AS max_rt,
		          SUM(CASE WHEN status='up' THEN 1 ELSE 0 END) AS up_count,
		          SUM(CASE WHEN status='down' THEN 1 ELSE 0 END) AS down_count,
		          COUNT(*) AS total
		   FROM check_results
		   WHERE created_at >= ? AND created_at < ?
		   GROUP BY monitor_id
		 )
		 SELECT agg.monitor_id, ? AS hour,
		        agg.sum_rt, agg.avg_rt, agg.min_rt, agg.max_rt,
		        COALESCE(p95.p95_rt, 0),
		        agg.up_count, agg.down_count, agg.total
		 FROM agg LEFT JOIN p95 ON p95.monitor_id = agg.monitor_id
		 WHERE ? >= COALESCE((SELECT value FROM meta WHERE key='raw_purged_before'), '')
		 OR NOT EXISTS (SELECT 1 FROM check_result_hourly h WHERE h.monitor_id=agg.monitor_id AND h.hour=?)
		 ON CONFLICT(monitor_id, hour) DO UPDATE SET `+rollupReplacement,
		hour+":00:00Z",
		hourEnd(hour)+":00:00Z",
		hour+":00:00Z",
		hourEnd(hour)+":00:00Z",
		hour, hour+":00:00Z", hour)
	return err
}

func (s *SQLiteStore) RollupDaily(ctx context.Context, day string) error {
	// Old hourly rows have only rounded means; weight those by their sample count.
	// New rows retain the exact sum so daily means do not compound rounding loss.
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO check_result_daily (monitor_id, day, sum_rt, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total)
		 SELECT monitor_id, ? AS day,
		        SUM(COALESCE(sum_rt, avg_rt * total)),
		        COALESCE(`+s.writeDB.dialect.integer("SUM(COALESCE(sum_rt, avg_rt * total)) / NULLIF(SUM(total), 0)")+`, 0),
		        MIN(min_rt),
		        MAX(max_rt),
		        MAX(p95_rt),
		        SUM(up_count),
		        SUM(down_count),
		        SUM(total)
		 FROM check_result_hourly h
		 WHERE hour >= ? AND hour < ?
		 AND (? >= COALESCE((SELECT value FROM meta WHERE key='hourly_purged_before'), '')
		      OR NOT EXISTS (SELECT 1 FROM check_result_daily d WHERE d.monitor_id=h.monitor_id AND d.day=?))
		 GROUP BY monitor_id
		 ON CONFLICT(monitor_id, day) DO UPDATE SET `+rollupReplacement,
		day,
		day+"T00",
		day+"T24", day+"T00", day)
	return err
}

func (s *SQLiteStore) PurgeHourlyBefore(ctx context.Context, before time.Time) (int64, error) {
	now := time.Now().UTC()
	if err := s.RollupPending(ctx, now); err != nil {
		return 0, err
	}
	// Retain whole source days. A durable floor prevents a restart or late
	// result from replacing a complete daily summary with partial hourly data.
	if before.After(now) {
		before = now
	}
	cutoff := before.UTC().Truncate(24 * time.Hour).Format("2006-01-02T15")
	if err := s.advancePurgeFloor(ctx, "hourly_purged_before", cutoff); err != nil {
		return 0, err
	}
	res, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM check_result_hourly WHERE hour < ?`,
		cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) PurgeDailyBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM check_result_daily WHERE day < ?`,
		before.Format("2006-01-02"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) GetTimeSeriesFromHourly(ctx context.Context, monitorID int64, from, to time.Time, maxPoints int, access ...MonitorAccess) ([]*TimeSeriesPoint, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	sampled, err := sampledMonitor(ctx, reader, monitorID)
	if err != nil {
		return nil, err
	}
	if !sampled {
		return []*TimeSeriesPoint{}, nil
	}
	rows, err := reader.QueryContext(ctx,
		`SELECT hour || ':00:00Z', avg_rt,
		        CASE WHEN down_count > 0 THEN 'down' WHEN total > up_count + down_count THEN 'degraded' ELSE 'up' END
		 FROM check_result_hourly
		 WHERE monitor_id=? AND hour >= ? AND hour < ?
		 ORDER BY hour`,
		monitorID, from.Format("2006-01-02T15"), to.Format("2006-01-02T15"))
	if err != nil {
		return nil, fmt.Errorf("hourly series: %w", err)
	}
	defer rows.Close()

	var points []*TimeSeriesPoint
	for rows.Next() {
		var p TimeSeriesPoint
		var ts string
		if err := rows.Scan(&ts, &p.ResponseTime, &p.Status); err != nil {
			return nil, err
		}
		p.Timestamp = parseTime(ts).Unix()
		points = append(points, &p)
	}
	return points, rows.Err()
}

func (s *SQLiteStore) GetTimeSeriesFromDaily(ctx context.Context, monitorID int64, from, to time.Time, access ...MonitorAccess) ([]*TimeSeriesPoint, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	sampled, err := sampledMonitor(ctx, reader, monitorID)
	if err != nil {
		return nil, err
	}
	if !sampled {
		return []*TimeSeriesPoint{}, nil
	}
	rows, err := reader.QueryContext(ctx,
		`SELECT day || 'T00:00:00Z', avg_rt,
		        CASE WHEN down_count > 0 THEN 'down' WHEN total > up_count + down_count THEN 'degraded' ELSE 'up' END
		 FROM check_result_daily
		 WHERE monitor_id=? AND day >= ? AND day < ?
		 ORDER BY day`,
		monitorID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("daily series: %w", err)
	}
	defer rows.Close()

	var points []*TimeSeriesPoint
	for rows.Next() {
		var p TimeSeriesPoint
		var ts string
		if err := rows.Scan(&ts, &p.ResponseTime, &p.Status); err != nil {
			return nil, err
		}
		p.Timestamp = parseTime(ts).Unix()
		points = append(points, &p)
	}
	return points, rows.Err()
}

func hourEnd(hour string) string {
	t, err := time.Parse("2006-01-02T15", hour)
	if err != nil {
		return hour
	}
	return t.Add(time.Hour).Format("2006-01-02T15")
}

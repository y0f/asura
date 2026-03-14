package storage

import (
	"context"
	"fmt"
	"time"
)

func (s *SQLiteStore) RollupHourly(ctx context.Context, hour string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT OR REPLACE INTO check_result_hourly (monitor_id, hour, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total)
		 SELECT monitor_id, ? AS hour,
		        CAST(AVG(response_time) AS INTEGER),
		        MIN(response_time),
		        MAX(response_time),
		        CAST(response_time AS INTEGER),
		        SUM(CASE WHEN status='up' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN status IN ('down','degraded') THEN 1 ELSE 0 END),
		        COUNT(*)
		 FROM check_results
		 WHERE created_at >= ? AND created_at < ?
		 GROUP BY monitor_id`,
		hour,
		hour+":00:00Z",
		hourEnd(hour)+":00:00Z")
	return err
}

func (s *SQLiteStore) RollupDaily(ctx context.Context, day string) error {
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT OR REPLACE INTO check_result_daily (monitor_id, day, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total)
		 SELECT monitor_id, ? AS day,
		        CAST(AVG(avg_rt) AS INTEGER),
		        MIN(min_rt),
		        MAX(max_rt),
		        MAX(p95_rt),
		        SUM(up_count),
		        SUM(down_count),
		        SUM(total)
		 FROM check_result_hourly
		 WHERE hour >= ? AND hour < ?
		 GROUP BY monitor_id`,
		day,
		day+"T00",
		day+"T24")
	return err
}

func (s *SQLiteStore) PurgeHourlyBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM check_result_hourly WHERE hour < ?`,
		before.Format("2006-01-02T15"))
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

func (s *SQLiteStore) GetTimeSeriesFromHourly(ctx context.Context, monitorID int64, from, to time.Time, maxPoints int) ([]*TimeSeriesPoint, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT hour || ':00:00Z', avg_rt,
		        CASE WHEN down_count > 0 THEN 'down' WHEN up_count > 0 THEN 'up' ELSE 'up' END
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

func (s *SQLiteStore) GetTimeSeriesFromDaily(ctx context.Context, monitorID int64, from, to time.Time) ([]*TimeSeriesPoint, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT day || 'T00:00:00Z', avg_rt,
		        CASE WHEN down_count > 0 THEN 'down' WHEN up_count > 0 THEN 'up' ELSE 'up' END
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

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

func (s *SQLiteStore) InsertContentChange(ctx context.Context, c *ContentChange) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO content_changes (monitor_id, old_hash, new_hash, diff, old_body, new_body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.MonitorID, c.OldHash, c.NewHash, c.Diff, c.OldBody, c.NewBody, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	c.ID = id
	c.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) ListContentChanges(ctx context.Context, monitorID int64, p Pagination) (*PaginatedResult, error) {
	var total int64
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM content_changes WHERE monitor_id=?", monitorID).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, monitor_id, old_hash, new_hash, diff, created_at
		 FROM content_changes WHERE monitor_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		monitorID, p.PerPage, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []*ContentChange
	for rows.Next() {
		var c ContentChange
		var createdAt string
		if err := rows.Scan(&c.ID, &c.MonitorID, &c.OldHash, &c.NewHash, &c.Diff, &createdAt); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(createdAt)
		changes = append(changes, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if changes == nil {
		changes = []*ContentChange{}
	}

	return &PaginatedResult{
		Data:       changes,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}

// --- Analytics ---

func (s *SQLiteStore) GetResponseTimeSeries(ctx context.Context, monitorID int64, from, to time.Time, maxPoints int) ([]*TimeSeriesPoint, error) {
	span := to.Sub(from)
	if span > 90*24*time.Hour {
		return s.GetTimeSeriesFromDaily(ctx, monitorID, from, to)
	}
	if span > 7*24*time.Hour {
		return s.GetTimeSeriesFromHourly(ctx, monitorID, from, to, maxPoints)
	}

	fromStr := formatTime(from)
	toStr := formatTime(to)

	var count int64
	err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id=? AND created_at >= ? AND created_at < ?`,
		monitorID, fromStr, toStr).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("count time series: %w", err)
	}

	var rows *sql.Rows
	if count <= int64(maxPoints) {
		rows, err = s.readDB.QueryContext(ctx,
			`SELECT created_at, response_time, status
			 FROM check_results
			 WHERE monitor_id=? AND created_at >= ? AND created_at < ?
			 ORDER BY created_at ASC, id ASC`,
			monitorID, fromStr, toStr)
	} else {
		bucketSecs := int64(to.Sub(from).Seconds()) / int64(maxPoints)
		if bucketSecs < 1 {
			bucketSecs = 1
		}
		rows, err = s.readDB.QueryContext(ctx,
			`SELECT MIN(created_at) as created_at,
			        CAST(AVG(response_time) AS INTEGER) as response_time,
			        CASE
			            WHEN SUM(CASE WHEN status='down' THEN 1 ELSE 0 END) > 0 THEN 'down'
			            WHEN SUM(CASE WHEN status='degraded' THEN 1 ELSE 0 END) > 0 THEN 'degraded'
			            ELSE 'up'
			        END as status
			 FROM check_results
			 WHERE monitor_id=? AND created_at >= ? AND created_at < ?
			 GROUP BY (CAST(strftime('%s', created_at) AS INTEGER) / ?)
			 ORDER BY created_at ASC`,
			monitorID, fromStr, toStr, bucketSecs)
	}
	if err != nil {
		return nil, fmt.Errorf("query time series: %w", err)
	}
	defer rows.Close()

	var points []*TimeSeriesPoint
	for rows.Next() {
		var createdAt string
		var rt int64
		var status string
		if err := rows.Scan(&createdAt, &rt, &status); err != nil {
			return nil, fmt.Errorf("scan time series: %w", err)
		}
		t := parseTime(createdAt)
		points = append(points, &TimeSeriesPoint{
			Timestamp:    t.Unix(),
			ResponseTime: rt,
			Status:       status,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate time series: %w", err)
	}
	if points == nil {
		points = []*TimeSeriesPoint{}
	}
	return points, nil
}

func (s *SQLiteStore) GetUptimePercent(ctx context.Context, monitorID int64, from, to time.Time) (float64, error) {
	from, to = from.UTC(), to.UTC()
	var total, up int64

	// Whole UTC days covered by the daily rollups are summed from
	// check_result_daily; only the edges outside the rolled-up days (the partial
	// first day, today, days not rolled up yet) are counted from raw rows. This
	// keeps long ranges (the 90-day status page figure) from scanning every check.
	raw := [][2]time.Time{{from, to}}
	firstDay := from.Truncate(24 * time.Hour)
	if firstDay.Before(from) {
		firstDay = firstDay.Add(24 * time.Hour)
	}
	if endDay := to.Truncate(24 * time.Hour); endDay.After(firstDay) {
		var minDay, maxDay sql.NullString
		err := s.readDB.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(total), 0), COALESCE(SUM(up_count), 0), MIN(day), MAX(day)
			 FROM check_result_daily WHERE monitor_id=? AND day >= ? AND day < ?`,
			monitorID, firstDay.Format("2006-01-02"), endDay.Format("2006-01-02")).Scan(&total, &up, &minDay, &maxDay)
		if err != nil {
			return 0, err
		}
		if minDay.Valid && maxDay.Valid {
			lo, err1 := time.Parse("2006-01-02", minDay.String)
			hi, err2 := time.Parse("2006-01-02", maxDay.String)
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("parse rollup day: %w", errors.Join(err1, err2))
			}
			raw = [][2]time.Time{{from, lo}, {hi.AddDate(0, 0, 1), to}}
		}
	}

	for _, r := range raw {
		if !r[0].Before(r[1]) {
			continue
		}
		var t, u int64
		err := s.readDB.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status='up' THEN 1 ELSE 0 END), 0)
			 FROM check_results WHERE monitor_id=? AND created_at >= ? AND created_at < ?`,
			monitorID, formatTime(r[0]), formatTime(r[1])).Scan(&t, &u)
		if err != nil {
			return 0, err
		}
		total += t
		up += u
	}
	if total == 0 {
		return 100, nil
	}
	return float64(up) / float64(total) * 100, nil
}

func (s *SQLiteStore) GetResponseTimePercentiles(ctx context.Context, monitorID int64, from, to time.Time) (p50, p95, p99 float64, err error) {
	err = s.readDB.QueryRowContext(ctx,
		`WITH s AS (
		   SELECT response_time,
		          ROW_NUMBER() OVER (ORDER BY response_time) AS rn,
		          COUNT(*) OVER ()                           AS n
		   FROM check_results
		   WHERE monitor_id=? AND created_at >= ? AND created_at < ? AND status='up'
		 )
		 SELECT
		   COALESCE(MIN(CASE WHEN rn >= n * 0.50 THEN response_time END), 0),
		   COALESCE(MIN(CASE WHEN rn >= n * 0.95 THEN response_time END), 0),
		   COALESCE(MIN(CASE WHEN rn >= n * 0.99 THEN response_time END), 0)
		 FROM s`,
		monitorID, formatTime(from), formatTime(to)).Scan(&p50, &p95, &p99)
	return
}

func (s *SQLiteStore) GetLatestResponseTimes(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT monitor_id, last_response_time FROM monitor_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]int64)
	for rows.Next() {
		var monitorID, rt int64
		if err := rows.Scan(&monitorID, &rt); err != nil {
			return nil, err
		}
		result[monitorID] = rt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SQLiteStore) GetCheckCounts(ctx context.Context, monitorID int64, from, to time.Time) (total, up, down, degraded int64, err error) {
	err = s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN status='up' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status='down' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status='degraded' THEN 1 ELSE 0 END), 0)
		 FROM check_results WHERE monitor_id=? AND created_at >= ? AND created_at < ?`,
		monitorID, formatTime(from), formatTime(to)).Scan(&total, &up, &down, &degraded)
	return
}

func (s *SQLiteStore) CountMonitorsByStatus(ctx context.Context) (up, down, degraded, paused int64, err error) {
	err = s.readDB.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN m.enabled=1 AND COALESCE(ms.status,'') NOT IN ('down','degraded') THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN m.enabled=1 AND ms.status='down' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN m.enabled=1 AND ms.status='degraded' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN m.enabled=0 THEN 1 ELSE 0 END), 0)
		 FROM monitors m
		 LEFT JOIN monitor_status ms ON ms.monitor_id = m.id`).
		Scan(&up, &down, &degraded, &paused)
	return
}

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

func (s *SQLiteStore) InsertContentChange(ctx context.Context, c *ContentChange) error {
	now := formatTime(time.Now())
	id, err := insertID(ctx, s.writeDB,
		`INSERT INTO content_changes (monitor_id, old_hash, new_hash, diff, old_body, new_body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.MonitorID, c.OldHash, c.NewHash, c.Diff, c.OldBody, c.NewBody, now)
	if err != nil {
		return err
	}
	c.ID = id
	c.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) ListContentChanges(ctx context.Context, monitorID int64, p Pagination, access ...MonitorAccess) (*PaginatedResult, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	var total int64
	err = reader.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM content_changes WHERE monitor_id=?", monitorID).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	rows, err := reader.QueryContext(ctx,
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

func (s *SQLiteStore) GetResponseTimeSeries(ctx context.Context, monitorID int64, from, to time.Time, maxPoints int, access ...MonitorAccess) ([]*TimeSeriesPoint, error) {
	span := to.Sub(from)
	if span > 90*24*time.Hour {
		return s.GetTimeSeriesFromDaily(ctx, monitorID, from, to, access...)
	}
	if span > 7*24*time.Hour {
		return s.GetTimeSeriesFromHourly(ctx, monitorID, from, to, maxPoints, access...)
	}

	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	fromStr := formatTime(from)
	toStr := formatTime(to)

	var count int64
	err = reader.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id=? AND created_at >= ? AND created_at < ?`,
		monitorID, fromStr, toStr).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("count time series: %w", err)
	}

	var rows *sql.Rows
	if count <= int64(maxPoints) {
		rows, err = reader.QueryContext(ctx,
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
		rows, err = reader.QueryContext(ctx,
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

func (s *SQLiteStore) GetUptimePercent(ctx context.Context, monitorID int64, from, to time.Time, access ...MonitorAccess) (float64, error) {
	days, err := s.GetDailyUptime(ctx, monitorID, from, to, access...)
	if err != nil {
		return 0, err
	}
	var total, up int64
	for _, day := range days {
		total += day.TotalChecks
		up += day.UpChecks
	}
	if total == 0 {
		return 100, nil
	}
	return float64(up) / float64(total) * 100, nil
}

func (s *SQLiteStore) GetResponseTimePercentiles(ctx context.Context, monitorID int64, from, to time.Time, access ...MonitorAccess) (p50, p95, p99 float64, err error) {
	result, err := s.GetResponseTimeSummary(ctx, monitorID, from, to, access...)
	if err != nil {
		return 0, 0, 0, err
	}
	return result.P50, result.P95, result.P99, nil
}

func (s *SQLiteStore) GetLatestResponseTimes(ctx context.Context, access ...MonitorAccess) (map[int64]int64, error) {
	clause, args := monitorAccessSQL("m", access)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT ms.monitor_id, ms.last_response_time FROM monitor_status ms
		 JOIN monitors m ON m.id = ms.monitor_id WHERE 1=1`+clause, args...)
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

func (s *SQLiteStore) GetCheckCounts(ctx context.Context, monitorID int64, from, to time.Time, access ...MonitorAccess) (total, up, down, degraded int64, err error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer release()
	err = reader.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN status='up' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status='down' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status='degraded' THEN 1 ELSE 0 END), 0)
		 FROM check_results WHERE monitor_id=? AND created_at >= ? AND created_at < ?`,
		monitorID, formatTime(from), formatTime(to)).Scan(&total, &up, &down, &degraded)
	return
}

func (s *SQLiteStore) CountMonitorsByStatus(ctx context.Context, access ...MonitorAccess) (up, down, degraded, paused int64, err error) {
	clause, args := monitorAccessSQL("m", access)
	err = s.readDB.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN m.enabled=1 AND COALESCE(ms.status,'') NOT IN ('down','degraded') THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN m.enabled=1 AND ms.status='down' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN m.enabled=1 AND ms.status='degraded' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN m.enabled=0 THEN 1 ELSE 0 END), 0)
		 FROM monitors m
		 LEFT JOIN monitor_status ms ON ms.monitor_id = m.id WHERE 1=1`+clause, args...).
		Scan(&up, &down, &degraded, &paused)
	return
}

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

func (s *SQLiteStore) InsertRequestLogBatch(ctx context.Context, logs []*RequestLog) error {
	if len(logs) == 0 {
		return nil
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("request log batch begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO request_logs (method, path, status_code, latency_ms, client_ip, user_agent, referer, monitor_id, route_group, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("request log batch prepare: %w", err)
	}
	defer stmt.Close()

	for _, l := range logs {
		var monitorID any
		if l.MonitorID != nil {
			monitorID = *l.MonitorID
		}
		_, err := stmt.ExecContext(ctx,
			l.Method, l.Path, l.StatusCode, l.LatencyMs,
			l.ClientIP, l.UserAgent, l.Referer,
			monitorID, l.RouteGroup, formatTime(l.CreatedAt))
		if err != nil {
			return fmt.Errorf("request log batch insert: %w", err)
		}
	}

	return tx.Commit()
}

func buildRequestLogWhere(f RequestLogFilter) (string, []any) {
	where := "1=1"
	var args []any

	if f.Method != "" {
		where += " AND method=?"
		args = append(args, f.Method)
	}
	if f.Path != "" {
		where += " AND path LIKE ?"
		args = append(args, "%"+f.Path+"%")
	}
	if f.StatusCode > 0 {
		where += " AND status_code=?"
		args = append(args, f.StatusCode)
	}
	if f.RouteGroup != "" {
		where += " AND route_group=?"
		args = append(args, f.RouteGroup)
	}
	if f.ClientIP != "" {
		where += " AND client_ip=?"
		args = append(args, f.ClientIP)
	}
	if f.MonitorID != nil {
		where += " AND monitor_id=?"
		args = append(args, *f.MonitorID)
	}
	if !f.From.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, formatTime(f.From))
	}
	if !f.To.IsZero() {
		where += " AND created_at < ?"
		args = append(args, formatTime(f.To))
	}
	return where, args
}

func (s *SQLiteStore) ListRequestLogs(ctx context.Context, f RequestLogFilter, p Pagination) (*PaginatedResult, error) {
	where, args := buildRequestLogWhere(f)

	var total int64
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM request_logs WHERE "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	args = append(args, p.PerPage, offset)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, method, path, status_code, latency_ms, client_ip, user_agent, referer, monitor_id, route_group, created_at
		 FROM request_logs WHERE `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*RequestLog
	for rows.Next() {
		var l RequestLog
		var monitorID sql.NullInt64
		var createdAt string
		err := rows.Scan(&l.ID, &l.Method, &l.Path, &l.StatusCode, &l.LatencyMs,
			&l.ClientIP, &l.UserAgent, &l.Referer, &monitorID, &l.RouteGroup, &createdAt)
		if err != nil {
			return nil, err
		}
		l.CreatedAt = parseTime(createdAt)
		if monitorID.Valid {
			mid := monitorID.Int64
			l.MonitorID = &mid
		}
		logs = append(logs, &l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if logs == nil {
		logs = []*RequestLog{}
	}

	return &PaginatedResult{
		Data:       logs,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}

func (s *SQLiteStore) ListTopClientIPs(ctx context.Context, from, to time.Time, limit int) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT client_ip FROM request_logs
		 WHERE created_at >= ? AND created_at < ? AND client_ip != ''
		 GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT ?`,
		formatTime(from), formatTime(to), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}

func (s *SQLiteStore) GetRequestLogStats(ctx context.Context, from, to time.Time) (*RequestLogStats, error) {
	fromStr := formatTime(from)
	toStr := formatTime(to)

	var stats RequestLogStats
	err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT client_ip), CAST(COALESCE(AVG(latency_ms), 0) AS INTEGER)
		 FROM request_logs WHERE created_at >= ? AND created_at < ?`,
		fromStr, toStr).Scan(&stats.TotalRequests, &stats.UniqueVisitors, &stats.AvgLatencyMs)
	if err != nil {
		return nil, err
	}

	topPaths, err := s.readDB.QueryContext(ctx,
		`SELECT path, COUNT(*) as cnt FROM request_logs
		 WHERE created_at >= ? AND created_at < ?
		 GROUP BY path ORDER BY cnt DESC LIMIT 10`, fromStr, toStr)
	if err != nil {
		return nil, err
	}
	defer topPaths.Close()

	for topPaths.Next() {
		var pc PathCount
		if err := topPaths.Scan(&pc.Path, &pc.Count); err != nil {
			return nil, err
		}
		stats.TopPaths = append(stats.TopPaths, pc)
	}
	if err := topPaths.Err(); err != nil {
		return nil, err
	}
	if stats.TopPaths == nil {
		stats.TopPaths = []PathCount{}
	}

	topReferers, err := s.readDB.QueryContext(ctx,
		`SELECT referer, COUNT(*) as cnt FROM request_logs
		 WHERE created_at >= ? AND created_at < ? AND referer != ''
		 GROUP BY referer ORDER BY cnt DESC LIMIT 10`, fromStr, toStr)
	if err != nil {
		return nil, err
	}
	defer topReferers.Close()

	for topReferers.Next() {
		var pc PathCount
		if err := topReferers.Scan(&pc.Path, &pc.Count); err != nil {
			return nil, err
		}
		stats.TopReferers = append(stats.TopReferers, pc)
	}
	if err := topReferers.Err(); err != nil {
		return nil, err
	}
	if stats.TopReferers == nil {
		stats.TopReferers = []PathCount{}
	}

	return &stats, nil
}

func (s *SQLiteStore) RollupRequestLogs(ctx context.Context, date string) error {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("rollup: invalid date %q: %w", date, err)
	}
	nextDay := d.AddDate(0, 0, 1).Format("2006-01-02")

	_, err = s.writeDB.ExecContext(ctx,
		`INSERT OR REPLACE INTO request_log_rollups (date, route_group, monitor_id, requests, unique_visitors, avg_latency_ms)
		 SELECT
		   ? AS date,
		   route_group,
		   monitor_id,
		   COUNT(*) AS requests,
		   COUNT(DISTINCT client_ip) AS unique_visitors,
		   CAST(COALESCE(AVG(latency_ms), 0) AS INTEGER) AS avg_latency_ms
		 FROM request_logs
		 WHERE created_at >= ? AND created_at < ?
		 GROUP BY route_group, monitor_id`,
		date, date+"T00:00:00Z", nextDay+"T00:00:00Z")
	return err
}

func (s *SQLiteStore) PurgeOldRequestLogs(ctx context.Context, before time.Time) (int64, error) {
	ts := formatTime(before)
	res, err := s.writeDB.ExecContext(ctx, "DELETE FROM request_logs WHERE created_at < ?", ts)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	res, err = s.writeDB.ExecContext(ctx, "DELETE FROM request_log_rollups WHERE date < ?", before.Format("2006-01-02"))
	if err != nil {
		return n, err
	}
	n2, _ := res.RowsAffected()
	return n + n2, nil
}

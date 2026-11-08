package storage

import (
	"context"
	"time"
)

// ResponseTimeSummary preserves whether successful checks supplied the
// percentiles. Zero milliseconds is a valid measured response time.
type ResponseTimeSummary struct {
	Samples       int64
	P50, P95, P99 float64
}

func (s *SQLiteStore) GetResponseTimeSummary(ctx context.Context, monitorID int64, from, to time.Time, access ...MonitorAccess) (*ResponseTimeSummary, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	return responseTimeSummaryQuery(ctx, reader, monitorID, from, to)
}

func responseTimeSummaryQuery(ctx context.Context, reader monitorReadQuery, monitorID int64, from, to time.Time) (*ResponseTimeSummary, error) {
	result := &ResponseTimeSummary{}
	sampled, err := sampledMonitor(ctx, reader, monitorID)
	if err != nil {
		return nil, err
	}
	if !sampled {
		return result, nil
	}
	err = reader.QueryRowContext(ctx,
		`WITH s AS (
		   SELECT response_time,
		          ROW_NUMBER() OVER (ORDER BY response_time) AS rn,
		          COUNT(*) OVER () AS n
		   FROM check_results
		   WHERE monitor_id=? AND observation_kind='probe' AND created_at >= ? AND created_at < ? AND status='up'
		 )
		 SELECT COUNT(*),
		   COALESCE(MIN(CASE WHEN rn >= n * 0.50 THEN response_time END), 0),
		   COALESCE(MIN(CASE WHEN rn >= n * 0.95 THEN response_time END), 0),
		   COALESCE(MIN(CASE WHEN rn >= n * 0.99 THEN response_time END), 0)
		 FROM s`,
		monitorID, formatTime(from), formatTime(to)).Scan(&result.Samples, &result.P50, &result.P95, &result.P99)
	if err != nil {
		return nil, err
	}
	return result, nil
}

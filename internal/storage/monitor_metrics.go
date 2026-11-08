package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/y0f/asura/internal/model"
)

var ErrNoMetricSamples = errors.New("no scheduled check samples available")

type MonitorSLASnapshot struct {
	Sampled         bool
	Target          float64
	Period, Rolling []*DailyUptime
}

// GetMonitorSLASnapshot binds the current monitor type, target, authority and
// both uptime windows to one database snapshot. A zero rollingFrom skips the
// second window for alert evaluation and monthly reports.
func (s *SQLiteStore) GetMonitorSLASnapshot(ctx context.Context, id int64, from, to, rollingFrom time.Time, access ...MonitorAccess) (*MonitorSLASnapshot, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := monitorSLASnapshotQuery(ctx, tx, id, from, to, rollingFrom, access...)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func monitorSLASnapshotQuery(ctx context.Context, reader monitorReadQuery, id int64, from, to, rollingFrom time.Time, access ...MonitorAccess) (*MonitorSLASnapshot, error) {
	var err error
	clause, args := monitorAccessSQL("", access)
	var monitor model.Monitor
	if err := reader.QueryRowContext(ctx, "SELECT type, sla_target FROM monitors WHERE id=?"+clause, append([]any{id}, args...)...).Scan(&monitor.Type, &monitor.SLATarget); err != nil {
		return nil, err
	}
	result := &MonitorSLASnapshot{Sampled: monitor.HasSampledMetrics(), Target: monitor.SLATarget}
	if result.Sampled {
		result.Period, err = dailyUptimeQuery(ctx, reader, id, from, to)
		if err != nil {
			return nil, err
		}
		if !rollingFrom.IsZero() {
			result.Rolling, err = dailyUptimeQuery(ctx, reader, id, rollingFrom, to)
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func sampledMonitor(ctx context.Context, reader monitorReadQuery, id int64) (bool, error) {
	var sampled bool
	err := reader.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM monitors WHERE id=? AND type NOT IN ('manual','heartbeat'))", id).Scan(&sampled)
	return sampled, err
}

type MonitorMetricsSnapshot struct {
	Sampled                   bool
	UptimePercent             *float64
	Total, Up, Down, Degraded int64
	Response                  *ResponseTimeSummary
}

// Read current type, authority and observations from one snapshot, without
// loading target credentials or combining raw counts with retained rollup rates.
func (s *SQLiteStore) GetMonitorMetricsSnapshot(ctx context.Context, id int64, from, to time.Time, access ...MonitorAccess) (*MonitorMetricsSnapshot, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := monitorMetricsSnapshotQuery(ctx, tx, id, from, to, access...)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func monitorMetricsSnapshotQuery(ctx context.Context, reader monitorReadQuery, id int64, from, to time.Time, access ...MonitorAccess) (*MonitorMetricsSnapshot, error) {
	clause, args := monitorAccessSQL("", access)
	var monitor model.Monitor
	if err := reader.QueryRowContext(ctx, "SELECT type FROM monitors WHERE id=?"+clause, append([]any{id}, args...)...).Scan(&monitor.Type); err != nil {
		return nil, err
	}
	result := &MonitorMetricsSnapshot{Sampled: monitor.HasSampledMetrics()}
	if result.Sampled {
		days, err := dailyUptimeQuery(ctx, reader, id, from, to)
		if err != nil {
			return nil, err
		}
		for _, day := range days {
			result.Total += day.TotalChecks
			result.Up += day.UpChecks
			result.Down += day.DownChecks
		}
		result.Degraded = result.Total - result.Up - result.Down
		if result.Total > 0 {
			percent := 100 * float64(result.Up) / float64(result.Total)
			result.UptimePercent = &percent
		}
		result.Response, err = responseTimeSummaryQuery(ctx, reader, id, from, to)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

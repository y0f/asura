package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PrometheusRow contains only metric dimensions and runtime counters.
// A monitor appears once per tag, or once with Tag nil when it has no tags.
// Rows are ordered by monitor ID, then tag ID; no settings or targets are read.
type PrometheusRow struct {
	MonitorID                    int64
	Name, Type, Status           string
	ConsecFails, ConsecSuccesses int64
	ResponseTime                 *int64
	Tag                          *MonitorTag
}

type PrometheusSummary struct {
	OpenIncidents, AcknowledgedIncidents, ResolvedIncidents int64
	Requests, UniqueVisitors, AvgLatencyMs                  int64
}

// VisitPrometheus reads every monitor and all aggregates in one snapshot.
// visit must not retain unbounded rows or perform network I/O. Its error aborts
// the snapshot, allowing callers to enforce their own output/resource budget.
func (s *SQLStore) VisitPrometheus(ctx context.Context, from, to time.Time, visit func(PrometheusRow) error) (*PrometheusSummary, error) {
	if visit == nil {
		return nil, errors.New("metrics visitor is required")
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	const batchSize = 256
	var cursor int64
	for {
		rows, err := tx.QueryContext(ctx, `WITH batch AS (
			SELECT m.id, m.name, m.type,
			 CASE WHEN m.enabled=0 THEN 'paused' ELSE COALESCE(ms.status, 'pending') END AS status,
			 COALESCE(ms.consec_fails, 0) AS fails, COALESCE(ms.consec_successes, 0) AS successes,
			 CASE WHEN ms.last_check_at IS NOT NULL AND m.type NOT IN ('manual', 'heartbeat')
			 THEN ms.last_response_time ELSE NULL END AS response_time
			FROM monitors m LEFT JOIN monitor_status ms ON ms.monitor_id=m.id
			WHERE m.id>? ORDER BY m.id LIMIT ?)
			SELECT b.id, b.name, b.type, b.status, b.fails, b.successes, b.response_time,
			 t.id, t.name, mt.value
			FROM batch b LEFT JOIN monitor_tags mt ON mt.monitor_id=b.id
			LEFT JOIN tags t ON t.id=mt.tag_id ORDER BY b.id, t.id`, cursor, batchSize)
		if err != nil {
			return nil, err
		}
		count, next, err := visitPrometheusRows(ctx, rows, visit)
		if err != nil {
			return nil, err
		}
		if count < batchSize {
			break
		}
		cursor = next
	}
	var summary PrometheusSummary
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status='open' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status='acknowledged' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status='resolved' THEN 1 ELSE 0 END), 0)
		FROM incidents`).Scan(&summary.OpenIncidents, &summary.AcknowledgedIncidents, &summary.ResolvedIncidents); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT client_ip), `+
		tx.dialect.integer("COALESCE(AVG(latency_ms), 0)")+`
		FROM request_logs WHERE created_at>=? AND created_at<?`, formatTime(from), formatTime(to)).
		Scan(&summary.Requests, &summary.UniqueVisitors, &summary.AvgLatencyMs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &summary, nil
}

func visitPrometheusRows(ctx context.Context, rows *sql.Rows, visit func(PrometheusRow) error) (int, int64, error) {
	defer rows.Close()
	count, lastID := 0, int64(0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		var row PrometheusRow
		var responseTime, tagID sql.NullInt64
		var tagName, tagValue sql.NullString
		if err := rows.Scan(&row.MonitorID, &row.Name, &row.Type, &row.Status, &row.ConsecFails,
			&row.ConsecSuccesses, &responseTime, &tagID, &tagName, &tagValue); err != nil {
			return 0, 0, err
		}
		if responseTime.Valid {
			row.ResponseTime = &responseTime.Int64
		}
		if tagID.Valid {
			row.Tag = &MonitorTag{TagID: tagID.Int64, Name: tagName.String, Value: tagValue.String}
		}
		if row.MonitorID != lastID {
			count++
			lastID = row.MonitorID
		}
		if err := visit(row); err != nil {
			return 0, 0, err
		}
	}
	return count, lastID, rows.Err()
}

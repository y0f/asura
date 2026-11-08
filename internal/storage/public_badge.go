package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PublicBadgeMetric string

const (
	PublicBadgeUptime   PublicBadgeMetric = "uptime"
	PublicBadgeResponse PublicBadgeMetric = "response"
	PublicBadgeCert     PublicBadgeMetric = "cert"
)

type PublicBadgeData struct {
	Monitor       *Monitor
	UptimePercent float64
	TotalChecks   int64
	Response      ResponseTimeSummary
	CertExpiry    *time.Time
}

// GetPublicBadgeMonitor reads only fields exposed by a public badge. The
// publication predicate is part of the same query; no settings are decrypted.
func (s *SQLiteStore) GetPublicBadgeMonitor(ctx context.Context, monitorID int64) (*Monitor, error) {
	return publicBadgeMonitorQuery(ctx, s.readDB, monitorID)
}

func publicBadgeMonitorQuery(ctx context.Context, reader monitorReadQuery, monitorID int64) (*Monitor, error) {
	m := &Monitor{}
	err := reader.QueryRowContext(ctx, `SELECT m.id,m.name,m.type,m.enabled,COALESCE(ms.status,'pending')
		FROM monitors m LEFT JOIN monitor_status ms ON ms.monitor_id=m.id
		WHERE m.id=? AND EXISTS (
			SELECT 1 FROM status_page_monitors spm JOIN status_pages sp ON sp.id=spm.page_id
			WHERE spm.monitor_id=m.id AND sp.enabled=1 AND sp.password_hash=''
		)`, monitorID).Scan(&m.ID, &m.Name, &m.Type, &m.Enabled, &m.Status)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// GetPublicBadgeData binds publication and only the requested metric to one
// read snapshot, so revocation cannot be combined with data from a later state.
func (s *SQLiteStore) GetPublicBadgeData(ctx context.Context, monitorID int64, metric PublicBadgeMetric, now time.Time) (*PublicBadgeData, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := publicBadgeDataQuery(ctx, tx, monitorID, metric, now.UTC())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func publicBadgeDataQuery(ctx context.Context, reader monitorReadQuery, monitorID int64, metric PublicBadgeMetric, now time.Time) (*PublicBadgeData, error) {
	m, err := publicBadgeMonitorQuery(ctx, reader, monitorID)
	if err != nil {
		return nil, err
	}
	result := &PublicBadgeData{Monitor: m}
	switch metric {
	case PublicBadgeUptime:
		days, err := dailyUptimeQuery(ctx, reader, monitorID, now.Add(-30*24*time.Hour), now)
		if err != nil {
			return nil, err
		}
		result.UptimePercent, result.TotalChecks = SummarizeUptime(days)
	case PublicBadgeResponse:
		if m.Type != "manual" && m.Type != "heartbeat" {
			summary, err := responseTimeSummaryQuery(ctx, reader, monitorID, now.Add(-24*time.Hour), now)
			if err != nil {
				return nil, err
			}
			result.Response = *summary
		}
	case PublicBadgeCert:
		var expiry sql.NullString
		err := reader.QueryRowContext(ctx, `SELECT cert_expiry FROM check_results
			WHERE monitor_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, monitorID).Scan(&expiry)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		result.CertExpiry = parseTimePtr(expiry)
	default:
		return nil, fmt.Errorf("unknown public badge metric")
	}
	return result, nil
}

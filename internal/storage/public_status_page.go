package storage

import (
	"context"
	"database/sql"
	"time"
)

type PublicStatusPageAudience uint8

const (
	PublicStatusPageWeb PublicStatusPageAudience = iota + 1
	PublicStatusPageJSON
	PublicStatusPageWebSubscription
	PublicStatusPageAPISubscription
)

// PublicStatusPageAccess is server-derived after checking the request's page
// credential. The current hash and visibility must still match inside the
// transaction which reads public data or creates a subscription.
type PublicStatusPageAccess struct {
	ExpectedPasswordHash string
	Audience             PublicStatusPageAudience
}

func (a PublicStatusPageAccess) predicate() string {
	switch a.Audience {
	case PublicStatusPageWeb, PublicStatusPageWebSubscription:
		return "enabled=1"
	case PublicStatusPageJSON:
		return "(enabled=1 OR api_enabled=1)"
	case PublicStatusPageAPISubscription:
		return "enabled=1 AND api_enabled=1"
	default:
		return "1=0"
	}
}

type PublicStatusPageSnapshot struct {
	Page        *StatusPage
	Monitors    []*Monitor
	Members     []StatusPageMonitor
	DailyUptime map[int64][]*DailyUptime
	Incidents   []*Incident
}

func publicStatusPageQuery(ctx context.Context, reader monitorReadQuery, id int64, access PublicStatusPageAccess) (*StatusPage, error) {
	var page StatusPage
	err := scanStatusPage(&page, reader.QueryRowContext(ctx, "SELECT "+statusPageColumns+
		" FROM status_pages WHERE id=? AND password_hash=? AND "+access.predicate(), id, access.ExpectedPasswordHash))
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// GetPublicStatusPageSnapshot binds publication, branding, members and their
// metrics/incidents to one snapshot. RSS skips the unused uptime queries.
func (s *SQLStore) GetPublicStatusPageSnapshot(ctx context.Context, id int64, access PublicStatusPageAccess, now time.Time, includeUptime bool) (*PublicStatusPageSnapshot, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	snapshot, err := publicStatusPageSnapshotQuery(ctx, tx, s.readDB.dialect, id, access, now.UTC(), includeUptime)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func publicStatusPageSnapshotQuery(ctx context.Context, reader monitorReadQuery, dialect sqlDialect, id int64, access PublicStatusPageAccess, now time.Time, includeUptime bool) (*PublicStatusPageSnapshot, error) {
	page, err := publicStatusPageQuery(ctx, reader, id, access)
	if err != nil {
		return nil, err
	}
	monitors, members, err := statusPageMonitorsQuery(ctx, reader, dialect, id)
	if err != nil {
		return nil, err
	}
	snapshot := &PublicStatusPageSnapshot{Page: page, Monitors: monitors, Members: members,
		DailyUptime: make(map[int64][]*DailyUptime), Incidents: []*Incident{}}
	if includeUptime {
		for _, monitor := range monitors {
			daily, err := dailyUptimeQuery(ctx, reader, monitor.ID, now.AddDate(0, 0, -90), now)
			if err != nil {
				return nil, err
			}
			snapshot.DailyUptime[monitor.ID] = daily
		}
	}
	if page.ShowIncidents && len(monitors) > 0 {
		// Public incidents omit diagnostics and account identities at the SQL
		// projection, so private values never enter the response snapshot.
		rows, err := reader.QueryContext(ctx, `SELECT i.id,i.monitor_id,i.status,i.severity,i.started_at,i.acknowledged_at,i.resolved_at,m.name
			FROM incidents i JOIN monitors m ON m.id=i.monitor_id
			JOIN status_page_monitors spm ON spm.monitor_id=m.id
			WHERE spm.page_id=? AND m.enabled=1
			AND NOT (i.status='resolved' AND i.resolved_at IS NOT NULL AND i.resolved_at<?)
			ORDER BY i.started_at DESC,i.id DESC LIMIT 10`, id, formatTime(now.AddDate(0, 0, -7)))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			incident := &Incident{}
			var started string
			var acknowledged, resolved sql.NullString
			if err := rows.Scan(&incident.ID, &incident.MonitorID, &incident.Status, &incident.Severity, &started, &acknowledged, &resolved, &incident.MonitorName); err != nil {
				rows.Close()
				return nil, err
			}
			incident.StartedAt, incident.AcknowledgedAt, incident.ResolvedAt = parseTime(started), parseTimePtr(acknowledged), parseTimePtr(resolved)
			snapshot.Incidents = append(snapshot.Incidents, incident)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

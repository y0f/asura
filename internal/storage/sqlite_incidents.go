package storage

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateIncident(ctx context.Context, inc *Incident) error {
	if inc.Severity == "" {
		inc.Severity = "critical"
	}
	now := formatTime(time.Now())
	id, err := insertID(ctx, s.writeDB,
		`INSERT INTO incidents (monitor_id, status, severity, cause, started_at, announcement_pending, escalation_pending) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		inc.MonitorID, inc.Status, inc.Severity, inc.Cause, now, boolToInt(inc.AnnouncementPending), boolToInt(inc.EscalationPending))
	if err != nil {
		return err
	}
	inc.ID = id
	inc.StartedAt = parseTime(now)
	return nil
}

const incidentColumns = `i.id, i.monitor_id, i.status, i.severity, i.cause, i.started_at,
		        i.acknowledged_at, i.acknowledged_by, i.resolved_at, i.resolved_by, i.announcement_pending, i.escalation_pending`

const incidentColumnsWithName = incidentColumns + `, COALESCE(m.name, ''), m.group_id`

func scanIncident(scan func(dest ...any) error, withName bool) (*Incident, error) {
	var inc Incident
	var startedAt string
	var ackAt, resAt sql.NullString
	var dest []any
	dest = append(dest, &inc.ID, &inc.MonitorID, &inc.Status, &inc.Severity, &inc.Cause, &startedAt,
		&ackAt, &inc.AcknowledgedBy, &resAt, &inc.ResolvedBy, &inc.AnnouncementPending, &inc.EscalationPending)
	if withName {
		dest = append(dest, &inc.MonitorName, &inc.MonitorGroupID)
	}
	if err := scan(dest...); err != nil {
		return nil, err
	}
	inc.StartedAt = parseTime(startedAt)
	inc.AcknowledgedAt = parseTimePtr(ackAt)
	inc.ResolvedAt = parseTimePtr(resAt)
	return &inc, nil
}

func (s *SQLiteStore) GetIncident(ctx context.Context, id int64, access ...MonitorAccess) (*Incident, error) {
	clause, args := monitorAccessSQL("m", access)
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+incidentColumnsWithName+`
		 FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		 WHERE i.id=?`+clause, append([]any{id}, args...)...)
	return scanIncident(row.Scan, true)
}

func (s *SQLiteStore) ListIncidents(ctx context.Context, monitorID int64, status string, search string, p Pagination) (*PaginatedResult, error) {
	return s.ListIncidentsFiltered(ctx, IncidentListFilter{MonitorID: monitorID, Status: status, Search: search}, p)
}

func (s *SQLiteStore) ListIncidentsFiltered(ctx context.Context, f IncidentListFilter, p Pagination) (*PaginatedResult, error) {
	where := "1=1"
	clause, args := monitorAccessSQL("m", f.Access)
	where += clause
	nameClause, nameArgs := monitorAccessSQL("m", f.MonitorReadAccess)
	if f.MonitorID > 0 {
		where += " AND i.monitor_id=?"
		args = append(args, f.MonitorID)
	}
	if f.Status != "" {
		where += " AND i.status=?"
		args = append(args, f.Status)
	}
	if f.Search != "" {
		where += " AND (((1=1" + nameClause + ") AND " + s.readDB.dialect.like("COALESCE(m.name, '')", "'%' || ? || '%'") + ") OR " + s.readDB.dialect.like("i.cause", "'%' || ? || '%'") + ")"
		args = append(args, nameArgs...)
		args = append(args, f.Search, f.Search)
	}

	var total int64
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id WHERE "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PerPage
	args = append(args, p.PerPage, offset)
	selectArgs := append(append([]any{}, nameArgs...), args...)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+incidentColumns+`, CASE WHEN (1=1`+nameClause+`) THEN COALESCE(m.name, '') ELSE '' END, m.group_id
		 FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		 WHERE `+where+` ORDER BY i.started_at DESC, i.id DESC LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []*Incident
	for rows.Next() {
		inc, err := scanIncident(rows.Scan, true)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if incidents == nil {
		incidents = []*Incident{}
	}

	return &PaginatedResult{
		Data:       incidents,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}

func (s *SQLiteStore) ListRecentIncidents(ctx context.Context, monitorIDs []int64, resolvedSince time.Time, limit int) ([]*Incident, error) {
	if len(monitorIDs) == 0 || limit <= 0 {
		return []*Incident{}, nil
	}
	placeholders := strings.Repeat("?,", len(monitorIDs))
	args := make([]any, 0, len(monitorIDs)+2)
	for _, id := range monitorIDs {
		args = append(args, id)
	}
	args = append(args, formatTime(resolvedSince), limit)

	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+incidentColumnsWithName+`
		 FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		 WHERE i.monitor_id IN (`+placeholders[:len(placeholders)-1]+`)
		   AND NOT (i.status='resolved' AND i.resolved_at IS NOT NULL AND i.resolved_at < ?)
		 ORDER BY i.started_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	incidents := []*Incident{}
	for rows.Next() {
		inc, err := scanIncident(rows.Scan, true)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, inc)
	}
	return incidents, rows.Err()
}

func (s *SQLiteStore) UpdateIncident(ctx context.Context, inc *Incident) error {
	var ackAt, resAt any
	if inc.AcknowledgedAt != nil {
		ackAt = formatTime(*inc.AcknowledgedAt)
	}
	if inc.ResolvedAt != nil {
		resAt = formatTime(*inc.ResolvedAt)
	}
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE incidents SET status=?, severity=?, cause=?, acknowledged_at=?, acknowledged_by=?, resolved_at=?, resolved_by=?,
		 announcement_pending=CASE WHEN ?='resolved' THEN 0 ELSE announcement_pending END,
		 escalation_pending=CASE WHEN ?='resolved' THEN 0 ELSE escalation_pending END WHERE id=?`,
		inc.Status, inc.Severity, inc.Cause, ackAt, inc.AcknowledgedBy, resAt, inc.ResolvedBy, inc.Status, inc.Status, inc.ID)
	return err
}

func (s *SQLiteStore) DeleteIncident(ctx context.Context, id int64, access ...MonitorAccess) error {
	clause, args := monitorOwnerAccessSQL("monitor_id", access)
	result, err := s.writeDB.ExecContext(ctx, "DELETE FROM incidents WHERE id=?"+clause, append([]any{id}, args...)...)
	return scopedMutationResult(result, err, access)
}

func (s *SQLiteStore) MarkIncidentAnnounced(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "UPDATE incidents SET announcement_pending=0 WHERE id=?", id)
	return err
}

func (s *SQLiteStore) GetOpenIncident(ctx context.Context, monitorID int64, access ...MonitorAccess) (*Incident, error) {
	clause, args := monitorOwnerAccessSQL("i.monitor_id", access)
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+incidentColumns+`
		 FROM incidents i WHERE i.monitor_id=? AND i.status IN ('open','acknowledged')`+clause+` ORDER BY i.started_at DESC LIMIT 1`,
		append([]any{monitorID}, args...)...)
	return scanIncident(row.Scan, false)
}

// --- Incident Events ---

func (s *SQLiteStore) InsertIncidentEvent(ctx context.Context, e *IncidentEvent) error {
	now := formatTime(time.Now())
	id, err := insertID(ctx, s.writeDB,
		`INSERT INTO incident_events (incident_id, type, message, created_at) VALUES (?, ?, ?, ?)`,
		e.IncidentID, e.Type, e.Message, now)
	if err != nil {
		return err
	}
	e.ID = id
	e.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) ListIncidentEvents(ctx context.Context, incidentID int64, access ...MonitorAccess) ([]*IncidentEvent, error) {
	var reader monitorReadQuery = s.readDB
	if len(access) > 0 {
		tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		clause, args := monitorAccessSQL("m", access)
		var present int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM incidents i JOIN monitors m ON m.id=i.monitor_id WHERE i.id=?`+clause, append([]any{incidentID}, args...)...).Scan(&present); err != nil {
			return nil, err
		}
		reader = tx
	}
	rows, err := reader.QueryContext(ctx,
		`SELECT id, incident_id, type, message, created_at
		 FROM incident_events WHERE incident_id=? ORDER BY created_at,id`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*IncidentEvent
	for rows.Next() {
		var e IncidentEvent
		var createdAt string
		if err := rows.Scan(&e.ID, &e.IncidentID, &e.Type, &e.Message, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(createdAt)
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if events == nil {
		events = []*IncidentEvent{}
	}
	return events, nil
}

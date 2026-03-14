package storage

import (
	"context"
	"database/sql"
	"math"
	"time"
)

func (s *SQLiteStore) CreateIncident(ctx context.Context, inc *Incident) error {
	if inc.Severity == "" {
		inc.Severity = "critical"
	}
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO incidents (monitor_id, status, severity, cause, started_at) VALUES (?, ?, ?, ?, ?)`,
		inc.MonitorID, inc.Status, inc.Severity, inc.Cause, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	inc.ID = id
	inc.StartedAt = parseTime(now)
	return nil
}

const incidentColumns = `i.id, i.monitor_id, i.status, i.severity, i.cause, i.started_at,
		        i.acknowledged_at, i.acknowledged_by, i.resolved_at, i.resolved_by`

const incidentColumnsWithName = incidentColumns + `, COALESCE(m.name, '')`

func scanIncident(scan func(dest ...any) error, withName bool) (*Incident, error) {
	var inc Incident
	var startedAt string
	var ackAt, resAt sql.NullString
	var dest []any
	dest = append(dest, &inc.ID, &inc.MonitorID, &inc.Status, &inc.Severity, &inc.Cause, &startedAt,
		&ackAt, &inc.AcknowledgedBy, &resAt, &inc.ResolvedBy)
	if withName {
		dest = append(dest, &inc.MonitorName)
	}
	if err := scan(dest...); err != nil {
		return nil, err
	}
	inc.StartedAt = parseTime(startedAt)
	inc.AcknowledgedAt = parseTimePtr(ackAt)
	inc.ResolvedAt = parseTimePtr(resAt)
	return &inc, nil
}

func (s *SQLiteStore) GetIncident(ctx context.Context, id int64) (*Incident, error) {
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+incidentColumnsWithName+`
		 FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		 WHERE i.id=?`, id)
	return scanIncident(row.Scan, true)
}

func (s *SQLiteStore) ListIncidents(ctx context.Context, monitorID int64, status string, search string, p Pagination) (*PaginatedResult, error) {
	where := "1=1"
	args := []any{}
	if monitorID > 0 {
		where += " AND i.monitor_id=?"
		args = append(args, monitorID)
	}
	if status != "" {
		where += " AND i.status=?"
		args = append(args, status)
	}
	if search != "" {
		where += " AND (COALESCE(m.name, '') LIKE '%' || ? || '%' OR i.cause LIKE '%' || ? || '%')"
		args = append(args, search, search)
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
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+incidentColumnsWithName+`
		 FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		 WHERE `+where+` ORDER BY i.started_at DESC LIMIT ? OFFSET ?`, args...)
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

func (s *SQLiteStore) UpdateIncident(ctx context.Context, inc *Incident) error {
	var ackAt, resAt any
	if inc.AcknowledgedAt != nil {
		ackAt = formatTime(*inc.AcknowledgedAt)
	}
	if inc.ResolvedAt != nil {
		resAt = formatTime(*inc.ResolvedAt)
	}
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE incidents SET status=?, severity=?, cause=?, acknowledged_at=?, acknowledged_by=?, resolved_at=?, resolved_by=? WHERE id=?`,
		inc.Status, inc.Severity, inc.Cause, ackAt, inc.AcknowledgedBy, resAt, inc.ResolvedBy, inc.ID)
	return err
}

func (s *SQLiteStore) DeleteIncident(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM incidents WHERE id=?", id)
	return err
}

func (s *SQLiteStore) GetOpenIncident(ctx context.Context, monitorID int64) (*Incident, error) {
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+incidentColumns+`
		 FROM incidents i WHERE i.monitor_id=? AND i.status IN ('open','acknowledged') ORDER BY i.started_at DESC LIMIT 1`,
		monitorID)
	return scanIncident(row.Scan, false)
}

// --- Incident Events ---

func (s *SQLiteStore) InsertIncidentEvent(ctx context.Context, e *IncidentEvent) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO incident_events (incident_id, type, message, created_at) VALUES (?, ?, ?, ?)`,
		e.IncidentID, e.Type, e.Message, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = id
	e.CreatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) ListIncidentEvents(ctx context.Context, incidentID int64) ([]*IncidentEvent, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, incident_id, type, message, created_at
		 FROM incident_events WHERE incident_id=? ORDER BY created_at`, incidentID)
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

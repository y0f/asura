package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrIncidentConflict = errors.New("incident state does not permit this action")

type IncidentTransition struct {
	Action   string
	Actor    string
	Severity string
}

func incidentSeverityRank(severity string) int {
	switch severity {
	case "warning":
		return 1
	case "minor":
		return 2
	case "major":
		return 3
	case "critical":
		return 4
	default:
		return 0
	}
}

func incidentByIDTx(ctx context.Context, tx *sql.Tx, id int64, access ...MonitorAccess) (*Incident, error) {
	clause, args := monitorAccessSQL("m", access)
	return scanIncident(tx.QueryRowContext(ctx, `SELECT `+incidentColumnsWithName+` FROM incidents i LEFT JOIN monitors m ON m.id=i.monitor_id WHERE i.id=?`+clause, append([]any{id}, args...)...).Scan, true)
}

func openIncidentTx(ctx context.Context, tx *sql.Tx, monitorID int64) (*Incident, error) {
	return scanIncident(tx.QueryRowContext(ctx, `SELECT `+incidentColumnsWithName+` FROM incidents i JOIN monitors m ON m.id=i.monitor_id WHERE i.monitor_id=? AND i.status IN ('open','acknowledged') ORDER BY i.started_at DESC,i.id DESC LIMIT 1`, monitorID).Scan, true)
}

func incidentEventTx(ctx context.Context, tx *sql.Tx, id int64, kind, message, now string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO incident_events (incident_id,type,message,created_at) VALUES (?,?,?,?)`, id, kind, message, now)
	return err
}

// TransitionIncident checks current ownership and changes only the columns
// belonging to the requested action. Timeline and escalation state commit with
// the transition; a failed event insert cannot leave an unrecorded mutation.
func (s *SQLiteStore) TransitionIncident(ctx context.Context, id int64, change IncidentTransition, access ...MonitorAccess) (*Incident, error) {
	if change.Action != "acknowledge" && change.Action != "resolve" && change.Action != "severity" {
		return nil, fmt.Errorf("unsupported incident action")
	}
	if change.Action == "severity" && incidentSeverityRank(change.Severity) == 0 {
		return nil, fmt.Errorf("invalid incident severity")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	inc, err := incidentByIDTx(ctx, tx, id, access...)
	if err != nil {
		return nil, err
	}
	if err := transitionIncidentTx(ctx, tx, inc, change, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return inc, nil
}

func transitionIncidentTx(ctx context.Context, tx *sql.Tx, inc *Incident, change IncidentTransition, recovery bool) error {
	now := formatTime(time.Now())
	var kind, message string
	switch change.Action {
	case "acknowledge":
		if inc.Status != "open" {
			return ErrIncidentConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE incidents SET status='acknowledged',acknowledged_at=?,acknowledged_by=?,escalation_pending=0 WHERE id=?`, now, change.Actor, inc.ID); err != nil {
			return err
		}
		at := parseTime(now)
		inc.Status, inc.AcknowledgedAt, inc.AcknowledgedBy, inc.EscalationPending = "acknowledged", &at, change.Actor, false
		kind, message = "acknowledged", "Acknowledged by "+change.Actor
	case "resolve":
		if inc.Status != "open" && inc.Status != "acknowledged" {
			return ErrIncidentConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE incidents SET status='resolved',resolved_at=?,resolved_by=?,announcement_pending=0,escalation_pending=0 WHERE id=?`, now, change.Actor, inc.ID); err != nil {
			return err
		}
		at := parseTime(now)
		inc.Status, inc.ResolvedAt, inc.ResolvedBy = "resolved", &at, change.Actor
		inc.AnnouncementPending, inc.EscalationPending = false, false
		kind, message = "resolved", "Manually resolved by "+change.Actor
		if recovery {
			kind, message = "check_recovered", "Monitor recovered, incident auto-resolved"
		}
	case "severity":
		if inc.Severity == change.Severity {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE incidents SET severity=? WHERE id=?`, change.Severity, inc.ID); err != nil {
			return err
		}
		kind, message = "severity_changed", "Severity changed from "+inc.Severity+" to "+change.Severity+" by "+change.Actor
		inc.Severity = change.Severity
	}
	if change.Action == "acknowledge" || change.Action == "resolve" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM escalation_states WHERE incident_id=?", inc.ID); err != nil {
			return err
		}
	}
	return incidentEventTx(ctx, tx, inc.ID, kind, message, now)
}

// RecordIncidentFailure serializes current-open lookup, creation or severity
// promotion, and timeline persistence. A failure after a manual resolution
// creates a new incident; it cannot restore a stale copy of the resolved row.
func (s *SQLiteStore) RecordIncidentFailure(ctx context.Context, monitorID int64, severity, cause string) (*Incident, bool, error) {
	if incidentSeverityRank(severity) == 0 {
		return nil, false, fmt.Errorf("invalid incident severity")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	inc, err := openIncidentTx(ctx, tx, monitorID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	created := errors.Is(err, sql.ErrNoRows)
	now := formatTime(time.Now())
	if created {
		result, err := tx.ExecContext(ctx, `INSERT INTO incidents (monitor_id,status,severity,cause,started_at,announcement_pending,escalation_pending) VALUES (?,'open',?,?,?,1,1)`, monitorID, severity, cause, now)
		if err != nil {
			return nil, false, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, false, err
		}
		if err := incidentEventTx(ctx, tx, id, "created", "Incident created: "+cause, now); err != nil {
			return nil, false, err
		}
		inc, err = incidentByIDTx(ctx, tx, id)
		if err != nil {
			return nil, false, err
		}
	} else {
		if err := incidentEventTx(ctx, tx, inc.ID, "check_failed", cause, now); err != nil {
			return nil, false, err
		}
		if incidentSeverityRank(severity) > incidentSeverityRank(inc.Severity) {
			if _, err := tx.ExecContext(ctx, "UPDATE incidents SET severity=? WHERE id=?", severity, inc.ID); err != nil {
				return nil, false, err
			}
			if err := incidentEventTx(ctx, tx, inc.ID, "severity_changed", "Severity raised from "+inc.Severity+" to "+severity, now); err != nil {
				return nil, false, err
			}
			inc.Severity = severity
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return inc, created, nil
}

// RecordIncidentRecovery resolves the current open incident under the writer
// lock. No open incident is a successful no-op, including a prior manual resolve.
// The returned event snapshot retains the prior AnnouncementPending decision so
// callers suppress recovery for an incident never announced. The stored resolved
// row clears both pending flags atomically with the timeline and cancellation.
func (s *SQLiteStore) RecordIncidentRecovery(ctx context.Context, monitorID int64) (*Incident, bool, error) {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	inc, err := openIncidentTx(ctx, tx, monitorID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	wasUnannounced := inc.AnnouncementPending
	if err := transitionIncidentTx(ctx, tx, inc, IncidentTransition{Action: "resolve", Actor: "auto"}, true); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	inc.AnnouncementPending = wasUnannounced
	return inc, true, nil
}

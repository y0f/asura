package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrStatusPageMonitorAccess   = errors.New("monitor read access is required for every affected status-page member")
	ErrStatusPageIncidentAccess  = errors.New("incident read access is required for every status-page member whose incidents are published")
	ErrInvalidStatusPageMonitors = errors.New("status-page monitors must have unique positive IDs, nonnegative order and section names of at most 255 characters")
	ErrStatusPageSlugConflict    = errors.New("slug already in use")
)

// StatusPagePrepare runs under the writer transaction. It must be pure and do
// no I/O. A nil current page denotes creation; nil memberships preserve the
// current set. The returned page must be a replacement, not a mutation of current.
type StatusPagePrepare func(current *StatusPage) (*StatusPage, *[]StatusPageMonitor, error)

// StatusPageAccess keeps the two publication authorities independent. Empty
// filters fail closed: page administration alone cannot publish monitor or
// incident data.
type StatusPageAccess struct {
	Monitors  []MonitorAccess
	Incidents []MonitorAccess
}

// SaveStatusPage atomically validates the current publication boundary and
// commits metadata, password and membership. A concurrent group move cannot
// turn a scoped read grant into publication authority over another monitor.
func (s *SQLiteStore) SaveStatusPage(ctx context.Context, id int64, prepare StatusPagePrepare, permissions StatusPageAccess) (*StatusPage, error) {
	if prepare == nil || id < 0 {
		return nil, fmt.Errorf("invalid status-page save")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	access := permissions.Monitors
	if len(access) == 0 {
		access = []MonitorAccess{{}}
	}
	incidentAccess := permissions.Incidents
	if len(incidentAccess) == 0 {
		incidentAccess = []MonitorAccess{{}}
	}
	var current *StatusPage
	if id != 0 {
		current = &StatusPage{}
		if err := scanStatusPage(current, tx.QueryRowContext(ctx, "SELECT "+statusPageColumns+" FROM status_pages WHERE id=?", id)); err != nil {
			return nil, err
		}
	}
	next, members, err := prepare(current)
	if err != nil {
		return nil, err
	}
	if next == nil || next == current {
		return nil, fmt.Errorf("status-page preparation must return a replacement")
	}
	saved := *next
	saved.ID = id
	if current != nil && (members != nil || statusPageExpandsExposure(current, &saved)) {
		clause, args := monitorAccessSQL("m", access)
		var total, allowed int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM status_page_monitors WHERE page_id=?", id).Scan(&total); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM status_page_monitors spm JOIN monitors m ON m.id=spm.monitor_id WHERE spm.page_id=?"+clause, append([]any{id}, args...)...).Scan(&allowed); err != nil {
			return nil, err
		}
		if total != allowed {
			return nil, ErrStatusPageMonitorAccess
		}
		if saved.ShowIncidents {
			clause, args = monitorAccessSQL("m", incidentAccess)
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM status_page_monitors spm JOIN monitors m ON m.id=spm.monitor_id WHERE spm.page_id=?"+clause, append([]any{id}, args...)...).Scan(&allowed); err != nil {
				return nil, err
			}
			if total != allowed {
				return nil, ErrStatusPageIncidentAccess
			}
		}
	}
	if members != nil {
		seen := make(map[int64]bool, len(*members))
		ids := make([]int64, 0, len(*members))
		if len(*members) > 10000 {
			return nil, ErrInvalidStatusPageMonitors
		}
		for _, member := range *members {
			if member.MonitorID <= 0 || seen[member.MonitorID] || member.SortOrder < 0 || len(member.GroupName) > 255 {
				return nil, ErrInvalidStatusPageMonitors
			}
			seen[member.MonitorID] = true
			ids = append(ids, member.MonitorID)
		}
		if len(ids) != 0 {
			placeholders, args := bulkArgs(ids)
			clause, accessArgs := monitorAccessSQL("m", access)
			var allowed int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM monitors m WHERE m.id IN ("+placeholders+")"+clause, append(args, accessArgs...)...).Scan(&allowed); err != nil {
				return nil, err
			}
			if allowed != len(ids) {
				return nil, ErrStatusPageMonitorAccess
			}
			if saved.ShowIncidents {
				clause, accessArgs = monitorAccessSQL("m", incidentAccess)
				if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM monitors m WHERE m.id IN ("+placeholders+")"+clause, append(args, accessArgs...)...).Scan(&allowed); err != nil {
					return nil, err
				}
				if allowed != len(ids) {
					return nil, ErrStatusPageIncidentAccess
				}
			}
		}
	}
	var occupied bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM status_pages WHERE slug=? AND id<>?)", saved.Slug, id).Scan(&occupied); err != nil {
		return nil, err
	}
	if occupied {
		return nil, ErrStatusPageSlugConflict
	}
	if err := saveStatusPage(ctx, tx, &saved, id == 0); err != nil {
		return nil, err
	}
	if members != nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM status_page_monitors WHERE page_id=?", saved.ID); err != nil {
			return nil, err
		}
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO status_page_monitors (page_id,monitor_id,sort_order,group_name) VALUES (?,?,?,?)")
		if err != nil {
			return nil, err
		}
		defer stmt.Close()
		for _, member := range *members {
			if _, err := stmt.ExecContext(ctx, saved.ID, member.MonitorID, member.SortOrder, member.GroupName); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &saved, nil
}

func statusPageExpandsExposure(old, next *StatusPage) bool {
	return (!old.Enabled && next.Enabled) || (!old.APIEnabled && next.APIEnabled) ||
		(!old.ShowIncidents && next.ShowIncidents) || old.Slug != next.Slug ||
		(old.PasswordHash != "" && old.PasswordHash != next.PasswordHash)
}

type statusPageExecutor interface {
	insertIDExecutor
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func saveStatusPage(ctx context.Context, db statusPageExecutor, sp *StatusPage, create bool) error {
	now := formatTime(time.Now())
	args := []any{sp.Slug, sp.Title, sp.Description, sp.CustomCSS, boolToInt(sp.ShowIncidents), boolToInt(sp.Enabled), boolToInt(sp.APIEnabled), sp.SortOrder, sp.LogoURL, sp.FaviconURL, sp.CustomHeaderHTML, sp.PasswordHash, sp.AnalyticsScript, now}
	if create {
		id, err := insertID(ctx, db, `INSERT INTO status_pages (slug,title,description,custom_css,show_incidents,enabled,api_enabled,sort_order,logo_url,favicon_url,custom_header_html,password_hash,analytics_script,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, append(args, now)...)
		if err != nil {
			return err
		}
		sp.ID = id
		sp.CreatedAt = parseTime(now)
	} else {
		if _, err := db.ExecContext(ctx, `UPDATE status_pages SET slug=?,title=?,description=?,custom_css=?,show_incidents=?,enabled=?,api_enabled=?,sort_order=?,logo_url=?,favicon_url=?,custom_header_html=?,password_hash=?,analytics_script=?,updated_at=? WHERE id=?`, append(args, sp.ID)...); err != nil {
			return err
		}
	}
	sp.UpdatedAt, sp.PasswordEnabled = parseTime(now), sp.PasswordHash != ""
	return nil
}

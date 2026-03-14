package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) IsMonitorOnStatusPage(ctx context.Context, monitorID int64) (bool, error) {
	var exists int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM status_page_monitors spm
			JOIN status_pages sp ON sp.id = spm.page_id
			WHERE spm.monitor_id = ? AND sp.enabled = 1
		)`, monitorID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is monitor on status page: %w", err)
	}
	return exists == 1, nil
}

func (s *SQLiteStore) GetDailyUptime(ctx context.Context, monitorID int64, from, to time.Time) ([]*DailyUptime, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT DATE(created_at) as day,
		        COUNT(*) as total,
		        COALESCE(SUM(CASE WHEN status='up' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status='down' THEN 1 ELSE 0 END), 0)
		 FROM check_results
		 WHERE monitor_id=? AND created_at >= ? AND created_at < ?
		 GROUP BY DATE(created_at)
		 ORDER BY day ASC`,
		monitorID, formatTime(from), formatTime(to))
	if err != nil {
		return nil, fmt.Errorf("get daily uptime: %w", err)
	}
	defer rows.Close()

	var results []*DailyUptime
	for rows.Next() {
		var d DailyUptime
		if err := rows.Scan(&d.Date, &d.TotalChecks, &d.UpChecks, &d.DownChecks); err != nil {
			return nil, err
		}
		if d.TotalChecks > 0 {
			d.UptimePct = float64(d.UpChecks) / float64(d.TotalChecks) * 100
		}
		results = append(results, &d)
	}
	return results, rows.Err()
}

// --- Status Pages ---

func (s *SQLiteStore) CreateStatusPage(ctx context.Context, sp *StatusPage) error {
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO status_pages
		 (slug, title, description, custom_css, show_incidents, enabled, api_enabled, sort_order,
		  logo_url, favicon_url, custom_header_html, password_hash, analytics_script, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.Slug, sp.Title, sp.Description, sp.CustomCSS, boolToInt(sp.ShowIncidents),
		boolToInt(sp.Enabled), boolToInt(sp.APIEnabled), sp.SortOrder,
		sp.LogoURL, sp.FaviconURL, sp.CustomHeaderHTML, sp.PasswordHash, sp.AnalyticsScript, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	sp.ID = id
	sp.CreatedAt = parseTime(now)
	sp.UpdatedAt = parseTime(now)
	return nil
}

func scanStatusPage(sp *StatusPage, row interface {
	Scan(...any) error
}) error {
	var createdAt, updatedAt string
	err := row.Scan(&sp.ID, &sp.Slug, &sp.Title, &sp.Description, &sp.CustomCSS,
		&sp.ShowIncidents, &sp.Enabled, &sp.APIEnabled, &sp.SortOrder,
		&sp.LogoURL, &sp.FaviconURL, &sp.CustomHeaderHTML, &sp.PasswordHash, &sp.AnalyticsScript,
		&createdAt, &updatedAt)
	if err != nil {
		return err
	}
	sp.CreatedAt = parseTime(createdAt)
	sp.UpdatedAt = parseTime(updatedAt)
	sp.PasswordEnabled = sp.PasswordHash != ""
	return nil
}

const statusPageColumns = `id, slug, title, description, custom_css, show_incidents, enabled, api_enabled, sort_order,
	logo_url, favicon_url, custom_header_html, password_hash, analytics_script, created_at, updated_at`

func (s *SQLiteStore) GetStatusPage(ctx context.Context, id int64) (*StatusPage, error) {
	var sp StatusPage
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+statusPageColumns+` FROM status_pages WHERE id=?`, id)
	if err := scanStatusPage(&sp, row); err != nil {
		return nil, err
	}
	return &sp, nil
}

func (s *SQLiteStore) GetStatusPageBySlug(ctx context.Context, slug string) (*StatusPage, error) {
	var sp StatusPage
	row := s.readDB.QueryRowContext(ctx,
		`SELECT `+statusPageColumns+` FROM status_pages WHERE slug=?`, slug)
	if err := scanStatusPage(&sp, row); err != nil {
		return nil, err
	}
	return &sp, nil
}

func (s *SQLiteStore) ListStatusPages(ctx context.Context) ([]*StatusPage, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT sp.id, sp.slug, sp.title, sp.description, sp.custom_css, sp.show_incidents,
		        sp.enabled, sp.api_enabled, sp.sort_order,
		        sp.logo_url, sp.favicon_url, sp.custom_header_html, sp.password_hash, sp.analytics_script,
		        sp.created_at, sp.updated_at, COALESCE(cnt.c, 0)
		 FROM status_pages sp
		 LEFT JOIN (SELECT page_id, COUNT(*) as c FROM status_page_monitors GROUP BY page_id) cnt ON cnt.page_id = sp.id
		 ORDER BY sp.sort_order, sp.title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pages []*StatusPage
	for rows.Next() {
		var sp StatusPage
		var createdAt, updatedAt string
		if err := rows.Scan(&sp.ID, &sp.Slug, &sp.Title, &sp.Description, &sp.CustomCSS,
			&sp.ShowIncidents, &sp.Enabled, &sp.APIEnabled, &sp.SortOrder,
			&sp.LogoURL, &sp.FaviconURL, &sp.CustomHeaderHTML, &sp.PasswordHash, &sp.AnalyticsScript,
			&createdAt, &updatedAt, &sp.MonitorCount); err != nil {
			return nil, err
		}
		sp.CreatedAt = parseTime(createdAt)
		sp.UpdatedAt = parseTime(updatedAt)
		sp.PasswordEnabled = sp.PasswordHash != ""
		pages = append(pages, &sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if pages == nil {
		pages = []*StatusPage{}
	}
	return pages, nil
}

func (s *SQLiteStore) UpdateStatusPage(ctx context.Context, sp *StatusPage) error {
	now := formatTime(time.Now())
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE status_pages SET slug=?, title=?, description=?, custom_css=?, show_incidents=?,
		 enabled=?, api_enabled=?, sort_order=?,
		 logo_url=?, favicon_url=?, custom_header_html=?, password_hash=?, analytics_script=?,
		 updated_at=? WHERE id=?`,
		sp.Slug, sp.Title, sp.Description, sp.CustomCSS, boolToInt(sp.ShowIncidents),
		boolToInt(sp.Enabled), boolToInt(sp.APIEnabled), sp.SortOrder,
		sp.LogoURL, sp.FaviconURL, sp.CustomHeaderHTML, sp.PasswordHash, sp.AnalyticsScript,
		now, sp.ID)
	return err
}

func (s *SQLiteStore) DeleteStatusPage(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM status_pages WHERE id=?", id)
	return err
}

func (s *SQLiteStore) SetStatusPageMonitors(ctx context.Context, pageID int64, monitors []StatusPageMonitor) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set status page monitors begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM status_page_monitors WHERE page_id=?`, pageID); err != nil {
		return err
	}

	if len(monitors) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO status_page_monitors (page_id, monitor_id, sort_order, group_name) VALUES (?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, m := range monitors {
			if _, err := stmt.ExecContext(ctx, pageID, m.MonitorID, m.SortOrder, m.GroupName); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) ListStatusPageMonitors(ctx context.Context, pageID int64) ([]StatusPageMonitor, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT page_id, monitor_id, sort_order, group_name FROM status_page_monitors WHERE page_id=? ORDER BY sort_order, monitor_id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []StatusPageMonitor
	for rows.Next() {
		var spm StatusPageMonitor
		if err := rows.Scan(&spm.PageID, &spm.MonitorID, &spm.SortOrder, &spm.GroupName); err != nil {
			return nil, err
		}
		result = append(result, spm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []StatusPageMonitor{}
	}
	return result, nil
}

func (s *SQLiteStore) ListStatusPageMonitorsWithStatus(ctx context.Context, pageID int64) ([]*Monitor, []StatusPageMonitor, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT m.id, m.name, m.description, m.type, m.target, m.interval_secs, m.timeout_secs, m.enabled,
		        m.tags, m.settings, m.assertions, m.track_changes, m.failure_threshold, m.success_threshold,
		        m.upside_down, m.resend_interval, m.sla_target, m.anomaly_sensitivity, m.group_id, m.proxy_id, m.escalation_policy_id, m.created_at, m.updated_at,
		        COALESCE(ms.status, 'pending'), ms.last_check_at, COALESCE(ms.consec_fails, 0), COALESCE(ms.consec_successes, 0),
		        spm.sort_order, spm.group_name
		 FROM status_page_monitors spm
		 JOIN monitors m ON m.id = spm.monitor_id
		 LEFT JOIN monitor_status ms ON ms.monitor_id = m.id
		 WHERE spm.page_id=? AND m.enabled=1
		 ORDER BY spm.sort_order, m.name COLLATE NOCASE`, pageID)
	if err != nil {
		return nil, nil, fmt.Errorf("list status page monitors with status: %w", err)
	}
	defer rows.Close()

	var monitors []*Monitor
	var spms []StatusPageMonitor
	for rows.Next() {
		var m Monitor
		var tagsStr, settingsStr, assertionsStr string
		var createdAt, updatedAt string
		var lastCheck sql.NullString
		var groupID, proxyID, escalationPolicyID sql.NullInt64
		var spmSortOrder int
		var spmGroupName string
		err := rows.Scan(&m.ID, &m.Name, &m.Description, &m.Type, &m.Target, &m.Interval, &m.Timeout, &m.Enabled,
			&tagsStr, &settingsStr, &assertionsStr, &m.TrackChanges, &m.FailureThreshold, &m.SuccessThreshold,
			&m.UpsideDown, &m.ResendInterval, &m.SLATarget, &m.AnomalySensitivity, &groupID, &proxyID, &escalationPolicyID, &createdAt, &updatedAt,
			&m.Status, &lastCheck, &m.ConsecFails, &m.ConsecSuccesses,
			&spmSortOrder, &spmGroupName)
		if err != nil {
			return nil, nil, err
		}
		if groupID.Valid {
			gid := groupID.Int64
			m.GroupID = &gid
		}
		if proxyID.Valid {
			pid := proxyID.Int64
			m.ProxyID = &pid
		}
		if escalationPolicyID.Valid {
			epid := escalationPolicyID.Int64
			m.EscalationPolicyID = &epid
		}
		m.CreatedAt = parseTime(createdAt)
		m.UpdatedAt = parseTime(updatedAt)
		json.Unmarshal([]byte(tagsStr), &m.Tags)
		m.Settings = json.RawMessage(settingsStr)
		m.Assertions = json.RawMessage(assertionsStr)
		m.LastCheckAt = parseTimePtr(lastCheck)
		if m.Tags == nil {
			m.Tags = []string{}
		}
		if !m.Enabled {
			m.Status = "paused"
		}
		if strings.TrimSpace(settingsStr) == "" {
			m.Settings = json.RawMessage("{}")
		}
		if strings.TrimSpace(assertionsStr) == "" {
			m.Assertions = json.RawMessage("[]")
		}
		monitors = append(monitors, &m)
		spms = append(spms, StatusPageMonitor{
			PageID:    pageID,
			MonitorID: m.ID,
			SortOrder: spmSortOrder,
			GroupName: spmGroupName,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if monitors == nil {
		monitors = []*Monitor{}
		spms = []StatusPageMonitor{}
	}
	return monitors, spms, nil
}

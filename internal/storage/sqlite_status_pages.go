package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

func (s *SQLiteStore) IsMonitorOnStatusPage(ctx context.Context, monitorID int64) (bool, error) {
	var exists int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM status_page_monitors spm
			JOIN status_pages sp ON sp.id = spm.page_id
			WHERE spm.monitor_id = ? AND sp.enabled = 1 AND sp.password_hash = ''
		)`, monitorID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is monitor on status page: %w", err)
	}
	return exists == 1, nil
}

func (s *SQLiteStore) GetDailyUptime(ctx context.Context, monitorID int64, from, to time.Time, access ...MonitorAccess) ([]*DailyUptime, error) {
	if !from.Before(to) {
		return []*DailyUptime{}, nil
	}
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := authorizeMonitorRead(ctx, tx, monitorID, access...); err != nil {
		return nil, err
	}
	results, err := dailyUptimeQuery(ctx, tx, monitorID, from, to)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

// dailyUptimeQuery reads rollups and raw gaps through a caller-owned snapshot.
func dailyUptimeQuery(ctx context.Context, reader monitorReadQuery, monitorID int64, from, to time.Time) ([]*DailyUptime, error) {
	from, to = from.UTC(), to.UTC()
	results := make([]*DailyUptime, 0)
	if !from.Before(to) {
		return results, nil
	}
	sampled, err := sampledMonitor(ctx, reader, monitorID)
	if err != nil {
		return nil, err
	}
	if !sampled {
		return results, nil
	}
	rollupEnd := to
	if today := time.Now().UTC().Truncate(24 * time.Hour); rollupEnd.After(today) {
		rollupEnd = today
	}
	firstDay := from.Truncate(24 * time.Hour)
	if firstDay.Before(from) {
		firstDay = firstDay.AddDate(0, 0, 1)
	}
	// Read rollups and uncovered raw intervals in one snapshot. Limiting the
	// raw scans to gaps keeps long reports proportional to unsummarized data.
	rows, err := reader.QueryContext(ctx,
		`SELECT day, total, up_count, down_count FROM check_result_daily
		 WHERE monitor_id=? AND observation_kind='probe' AND day>=? AND day<? ORDER BY day`,
		monitorID, firstDay.Format("2006-01-02"), rollupEnd.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("get daily uptime rollup: %w", err)
	}
	var gaps [][2]time.Time
	cursor := from
	for rows.Next() {
		var d DailyUptime
		if err := rows.Scan(&d.Date, &d.TotalChecks, &d.UpChecks, &d.DownChecks); err != nil {
			rows.Close()
			return nil, err
		}
		day, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("parse rollup day: %w", err)
		}
		if cursor.Before(day) {
			gaps = append(gaps, [2]time.Time{cursor, day})
		}
		cursor = day.AddDate(0, 0, 1)
		results = append(results, &d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if cursor.Before(to) {
		gaps = append(gaps, [2]time.Time{cursor, to})
	}
	for _, gap := range gaps {
		rows, err := reader.QueryContext(ctx,
			`SELECT SUBSTR(created_at,1,10) AS day, COUNT(*),
			 COALESCE(SUM(CASE WHEN status='up' THEN 1 ELSE 0 END),0),
			 COALESCE(SUM(CASE WHEN status='down' THEN 1 ELSE 0 END),0)
			 FROM check_results WHERE monitor_id=? AND observation_kind='probe' AND created_at>=? AND created_at<?
			 GROUP BY SUBSTR(created_at,1,10)`, monitorID, formatTime(gap[0]), formatTime(gap[1]))
		if err != nil {
			return nil, fmt.Errorf("get daily uptime gap: %w", err)
		}
		for rows.Next() {
			var d DailyUptime
			if err := rows.Scan(&d.Date, &d.TotalChecks, &d.UpChecks, &d.DownChecks); err != nil {
				rows.Close()
				return nil, err
			}
			results = append(results, &d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, d := range results {
		if d.TotalChecks > 0 {
			d.UptimePct = float64(d.UpChecks) / float64(d.TotalChecks) * 100
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Date < results[j].Date })
	return results, nil
}

// --- Status Pages ---

func (s *SQLiteStore) CreateStatusPage(ctx context.Context, sp *StatusPage) error {
	return saveStatusPage(ctx, s.writeDB, sp, true)
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
		 ORDER BY sp.sort_order, `+s.readDB.dialect.noCase("sp.title"))
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
	return saveStatusPage(ctx, s.writeDB, sp, false)
}

func (s *SQLiteStore) DeleteStatusPage(ctx context.Context, id int64) error {
	result, err := s.writeDB.ExecContext(ctx, "DELETE FROM status_pages WHERE id=?", id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
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

func (s *SQLiteStore) ListStatusPageMonitors(ctx context.Context, pageID int64, access ...MonitorAccess) ([]StatusPageMonitor, error) {
	clause, args := monitorAccessSQL("m", access)
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT spm.page_id, spm.monitor_id, spm.sort_order, spm.group_name FROM status_page_monitors spm JOIN monitors m ON m.id=spm.monitor_id WHERE spm.page_id=?`+clause+` ORDER BY spm.sort_order, spm.monitor_id`, append([]any{pageID}, args...)...)
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

// ListStatusPageMonitorsWithStatus returns only public display fields. Targets,
// settings, credentials and assertions are never loaded or decrypted here.
func (s *SQLiteStore) ListStatusPageMonitorsWithStatus(ctx context.Context, pageID int64) ([]*Monitor, []StatusPageMonitor, error) {
	return statusPageMonitorsQuery(ctx, s.readDB, s.readDB.dialect, pageID)
}

func statusPageMonitorsQuery(ctx context.Context, reader monitorReadQuery, dialect sqlDialect, pageID int64) ([]*Monitor, []StatusPageMonitor, error) {
	rows, err := reader.QueryContext(ctx, `SELECT m.id,m.name,m.type,COALESCE(ms.status,'pending'),spm.sort_order,spm.group_name
		FROM status_page_monitors spm JOIN monitors m ON m.id=spm.monitor_id
		LEFT JOIN monitor_status ms ON ms.monitor_id=m.id
		WHERE spm.page_id=? AND m.enabled=1 ORDER BY spm.sort_order,`+dialect.noCase("m.name")+``, pageID)
	if err != nil {
		return nil, nil, fmt.Errorf("list status page monitors with status: %w", err)
	}
	defer rows.Close()
	monitors := []*Monitor{}
	members := []StatusPageMonitor{}
	for rows.Next() {
		m := &Monitor{Enabled: true}
		member := StatusPageMonitor{PageID: pageID}
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Status, &member.SortOrder, &member.GroupName); err != nil {
			return nil, nil, err
		}
		member.MonitorID = m.ID
		monitors = append(monitors, m)
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return monitors, members, nil
}

func (s *SQLiteStore) UpgradeStatusPagePasswordHash(ctx context.Context, id int64, oldHash, newHash string) (bool, error) {
	if oldHash == "" || newHash == "" {
		return false, nil
	}
	res, err := s.writeDB.ExecContext(ctx, "UPDATE status_pages SET password_hash=? WHERE id=? AND password_hash=?", newHash, id, oldHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

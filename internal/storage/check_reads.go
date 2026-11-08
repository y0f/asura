package storage

import (
	"context"
	"database/sql"
	"math"
)

// Summary queries do not load private response text into application memory.
// Their full counterparts retain diagnostic evidence for authorized callers.
func checkReadColumns(diagnostics bool) string {
	private := "c.message, c.headers, c.body, c.body_hash, c.cert_fingerprint, c.dns_records"
	if !diagnostics {
		private = "'', '', '', '', '', ''"
	}
	return "c.id, c.monitor_id, c.observation_kind, c.status, c.response_time, c.status_code, " + private + ", c.cert_expiry, c.agent_id, COALESCE(a.name,''), c.created_at"
}

func scanCheckResult(scan func(...any) error) (*CheckResult, error) {
	var result CheckResult
	var expiry sql.NullString
	var created string
	if err := scan(&result.ID, &result.MonitorID, &result.ObservationKind, &result.Status, &result.ResponseTime, &result.StatusCode,
		&result.Message, &result.Headers, &result.Body, &result.BodyHash, &result.CertFingerprint, &result.DNSRecords,
		&expiry, &result.AgentID, &result.AgentName, &created); err != nil {
		return nil, err
	}
	result.CertExpiry, result.CreatedAt = parseTimePtr(expiry), parseTime(created)
	return &result, nil
}

func (s *SQLiteStore) ListCheckResults(ctx context.Context, monitorID int64, p Pagination, access ...MonitorAccess) (*PaginatedResult, error) {
	return s.listCheckResults(ctx, monitorID, p, true, access...)
}

func (s *SQLiteStore) ListCheckSummaries(ctx context.Context, monitorID int64, p Pagination, access ...MonitorAccess) (*PaginatedResult, error) {
	return s.listCheckResults(ctx, monitorID, p, false, access...)
}

func (s *SQLiteStore) listCheckResults(ctx context.Context, monitorID int64, p Pagination, diagnostics bool, access ...MonitorAccess) (*PaginatedResult, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	var total int64
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM check_results WHERE monitor_id=?", monitorID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := reader.QueryContext(ctx, `SELECT `+checkReadColumns(diagnostics)+`
		FROM check_results c LEFT JOIN agents a ON a.id=c.agent_id
		WHERE c.monitor_id=? ORDER BY c.created_at DESC, c.id DESC LIMIT ? OFFSET ?`, monitorID, p.PerPage, (p.Page-1)*p.PerPage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []*CheckResult{}
	for rows.Next() {
		result, err := scanCheckResult(rows.Scan)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &PaginatedResult{Data: results, Total: total, Page: p.Page, PerPage: p.PerPage, TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage)))}, nil
}

func (s *SQLiteStore) GetLatestCheckResult(ctx context.Context, monitorID int64, access ...MonitorAccess) (*CheckResult, error) {
	return s.latestCheckResult(ctx, monitorID, true, access...)
}

func (s *SQLiteStore) GetLatestCheckSummary(ctx context.Context, monitorID int64, access ...MonitorAccess) (*CheckResult, error) {
	return s.latestCheckResult(ctx, monitorID, false, access...)
}

func (s *SQLiteStore) latestCheckResult(ctx context.Context, monitorID int64, diagnostics bool, access ...MonitorAccess) (*CheckResult, error) {
	reader, release, err := s.monitorReader(ctx, monitorID, access...)
	if err != nil {
		return nil, err
	}
	defer release()
	return scanCheckResult(reader.QueryRowContext(ctx, `SELECT `+checkReadColumns(diagnostics)+`
		FROM check_results c LEFT JOIN agents a ON a.id=c.agent_id
		WHERE c.monitor_id=? ORDER BY c.created_at DESC, c.id DESC LIMIT 1`, monitorID).Scan)
}

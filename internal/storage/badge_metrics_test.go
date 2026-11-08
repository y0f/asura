package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResponseSummaryCountsSuccessfulSamplesInSameWindow(t *testing.T) {
	s := testStore(t)
	m := newTestMonitor(t, s, "measured zero")
	ctx := context.Background()
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	got, err := s.GetResponseTimeSummary(ctx, m.ID, from, to)
	if err != nil || got.Samples != 0 || got.P50 != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	insertRollupCheck(t, s, m.ID, from, "up", 0)
	insertRollupCheck(t, s, m.ID, from.Add(time.Minute), "down", 9000)
	insertRollupCheck(t, s, m.ID, from.Add(2*time.Minute), "degraded", 8000)
	insertRollupCheck(t, s, m.ID, to, "up", 7000)
	insertRollupCheck(t, s, m.ID, from.Add(-time.Second), "up", 6000)
	got, err = s.GetResponseTimeSummary(ctx, m.ID, from, to, MonitorAccess{MonitorIDs: []int64{m.ID}})
	if err != nil || got.Samples != 1 || got.P50 != 0 || got.P95 != 0 || got.P99 != 0 {
		t.Fatalf("zero duration is measured; failed/outside checks excluded: %+v %v", got, err)
	}
	insertRollupCheck(t, s, m.ID, from.Add(3*time.Minute), "up", 20)
	insertRollupCheck(t, s, m.ID, from.Add(4*time.Minute), "up", 40)
	got, err = s.GetResponseTimeSummary(ctx, m.ID, from, to)
	if err != nil || got.Samples != 3 || got.P50 != 20 || got.P95 != 40 || got.P99 != 40 {
		t.Fatalf("percentiles: %+v %v", got, err)
	}
	if _, err := s.GetResponseTimeSummary(ctx, m.ID, from, to, MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("denied scope returned metrics: %v", err)
	}
	p50, p95, p99, err := s.GetResponseTimePercentiles(ctx, m.ID, from, to)
	if err != nil || p50 != got.P50 || p95 != got.P95 || p99 != got.P99 {
		t.Fatalf("compatible percentile wrapper: %v %v %v %v", p50, p95, p99, err)
	}
}

func TestPublicBadgeProjectionRequiresUnprotectedPublication(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := newTestMonitor(t, s, "Published service")
	sp := &StatusPage{Title: "Services", Slug: "services", Enabled: false, APIEnabled: true}
	if err := s.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatusPageMonitors(ctx, sp.ID, []StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	// Corrupt private settings are deliberately not read or decrypted by badges.
	if _, err := s.writeDB.Exec(`UPDATE monitors SET target='https://private.test',settings='enc:not-readable',assertions='private' WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		name, password string
		enabled        bool
		public         bool
	}{
		{name: "API only"},
		{name: "password protected", enabled: true, password: "stored-hash"},
		{name: "public", enabled: true, public: true},
	} {
		t.Run(state.name, func(t *testing.T) {
			if _, err := s.writeDB.Exec(`UPDATE status_pages SET enabled=?,password_hash=? WHERE id=?`, boolToInt(state.enabled), state.password, sp.ID); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetPublicBadgeMonitor(ctx, m.ID)
			if !state.public {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("unpublished metadata exposed: %+v %v", got, err)
				}
				return
			}
			if err != nil || got.ID != m.ID || got.Name != m.Name || got.Type != m.Type || got.Status != "pending" || got.Target != "" || len(got.Settings) != 0 || len(got.Assertions) != 0 {
				t.Fatalf("public projection: %+v %v", got, err)
			}
		})
	}
	if _, err := s.GetPublicBadgeMonitor(ctx, m.ID+1000); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing monitor: %v", err)
	}
	if _, err := s.writeDB.Exec(`DELETE FROM status_page_monitors WHERE page_id=?`, sp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPublicBadgeMonitor(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed membership still public: %v", err)
	}
}

// Interpose only between publication and metric reads. All production reads
// still execute against the actual read transaction while a writer commits.
type badgeSnapshotReader struct {
	monitorReadQuery
	beforeMetric func()
	queries      []string
}

func (r *badgeSnapshotReader) before(query string) {
	r.queries = append(r.queries, query)
	if strings.Contains(query, "check_result") && r.beforeMetric != nil {
		callback := r.beforeMetric
		r.beforeMetric = nil
		callback()
	}
}
func (r *badgeSnapshotReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.before(query)
	return r.monitorReadQuery.QueryContext(ctx, query, args...)
}
func (r *badgeSnapshotReader) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.before(query)
	return r.monitorReadQuery.QueryRowContext(ctx, query, args...)
}

func TestPublicBadgeDataUsesPublicationSnapshot(t *testing.T) {
	for _, metric := range []PublicBadgeMetric{PublicBadgeUptime, PublicBadgeResponse, PublicBadgeCert} {
		t.Run(string(metric), func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			m := newTestMonitor(t, s, "service")
			sp := &StatusPage{Title: "Public", Slug: "public", Enabled: true}
			if err := s.CreateStatusPage(ctx, sp); err != nil {
				t.Fatal(err)
			}
			if err := s.SetStatusPageMonitors(ctx, sp.ID, []StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			expiry := now.Add(24 * time.Hour)
			if _, err := s.writeDB.Exec(`INSERT INTO check_results(monitor_id,status,response_time,cert_expiry,created_at,observation_kind) VALUES (?,'up',0,?,?,'probe')`, m.ID, formatTime(expiry), formatTime(now.Add(-2*time.Minute))); err != nil {
				t.Fatal(err)
			}
			tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			reader := &badgeSnapshotReader{monitorReadQuery: tx, beforeMetric: func() {
				if _, err := s.writeDB.Exec(`UPDATE status_pages SET password_hash='now-protected' WHERE id=?`, sp.ID); err != nil {
					t.Fatal(err)
				}
				status := "up"
				if metric == PublicBadgeUptime {
					status = "down"
				}
				if _, err := s.writeDB.Exec(`INSERT INTO check_results(monitor_id,status,response_time,cert_expiry,created_at,observation_kind) VALUES (?,?,999,?,?,'probe')`, m.ID, status, formatTime(expiry.Add(10*24*time.Hour)), formatTime(now.Add(-time.Minute))); err != nil {
					t.Fatal(err)
				}
			}}
			data, err := publicBadgeDataQuery(ctx, reader, m.ID, metric, now)
			if err != nil {
				t.Fatal(err)
			}
			if reader.beforeMetric != nil {
				t.Fatal("test did not interleave the writer")
			}
			switch metric {
			case PublicBadgeUptime:
				if data.TotalChecks != 1 || data.UptimePercent != 100 {
					t.Fatalf("mixed publication/data states: %+v", data)
				}
			case PublicBadgeResponse:
				if data.Response.Samples != 1 || data.Response.P99 != 0 {
					t.Fatalf("mixed publication/data states: %+v", data)
				}
			case PublicBadgeCert:
				if data.CertExpiry == nil || !data.CertExpiry.Equal(expiry) {
					t.Fatalf("mixed publication/data states: %+v", data)
				}
				if len(reader.queries) != 2 || !strings.HasPrefix(reader.queries[1], "SELECT cert_expiry FROM") {
					t.Fatalf("certificate projection loaded unnecessary columns: %v", reader.queries)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetPublicBadgeData(ctx, m.ID, metric, now); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("revoked publication returned data: %v", err)
			}
		})
	}
}

func TestPublicPassiveResponseBadgeSkipsMetricQuery(t *testing.T) {
	for _, kind := range []string{"heartbeat", "manual"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			m := newTestMonitor(t, s, kind)
			if _, err := s.writeDB.Exec(`UPDATE monitors SET type=? WHERE id=?`, kind, m.ID); err != nil {
				t.Fatal(err)
			}
			sp := &StatusPage{Title: "Public", Slug: "public", Enabled: true}
			if err := s.CreateStatusPage(ctx, sp); err != nil {
				t.Fatal(err)
			}
			if err := s.SetStatusPageMonitors(ctx, sp.ID, []StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
				t.Fatal(err)
			}
			reader := &badgeSnapshotReader{monitorReadQuery: s.readDB}
			data, err := publicBadgeDataQuery(ctx, reader, m.ID, PublicBadgeResponse, time.Now())
			if err != nil || data.Monitor.Type != kind || len(reader.queries) != 1 {
				t.Fatalf("passive duration queried: %+v %v %v", data, reader.queries, err)
			}
		})
	}
}

package httputil

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type countingStore struct {
	storage.Store
	calls atomic.Int64
}

func (c *countingStore) ListIncidents(ctx context.Context, monitorID int64, status, search string, p storage.Pagination) (*storage.PaginatedResult, error) {
	c.calls.Add(1)
	return c.Store.ListIncidents(ctx, monitorID, status, search, p)
}

func (c *countingStore) ListRecentIncidents(ctx context.Context, ids []int64, since time.Time, limit int) ([]*storage.Incident, error) {
	c.calls.Add(1)
	return c.Store.ListRecentIncidents(ctx, ids, since, limit)
}

const tsLayout = "2006-01-02T15:04:05Z"

// incidentStore returns a store with a page monitor and another monitor that
// has 5000 old resolved incidents.
func incidentStore(tb testing.TB) (*countingStore, *storage.Monitor, *sql.DB) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "p.db")
	st, err := storage.NewSQLiteStore(path, 4)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { st.Close() })
	ctx := context.Background()
	pageMon := &storage.Monitor{Name: "page", Type: "http", Target: "https://a.example", Interval: 60, Timeout: 5, Enabled: true}
	other := &storage.Monitor{Name: "other", Type: "http", Target: "https://b.example", Interval: 60, Timeout: 5, Enabled: true}
	for _, m := range []*storage.Monitor{pageMon, other} {
		if err := st.CreateMonitor(ctx, m); err != nil {
			tb.Fatal(err)
		}
	}
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { raw.Close() })
	tx, err := raw.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO incidents (monitor_id, status, started_at, resolved_at) VALUES (?, 'resolved', ?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	base := time.Now().UTC().AddDate(0, 0, -60)
	for i := 0; i < 5000; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		if _, err := stmt.Exec(other.ID, ts.Format(tsLayout), ts.Add(time.Minute).Format(tsLayout)); err != nil {
			tb.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return &countingStore{Store: st}, pageMon, raw
}

func TestPublicIncidentsForPageSingleQuery(t *testing.T) {
	cs, mon, _ := incidentStore(t)
	sp := &storage.StatusPage{ShowIncidents: true}
	got := PublicIncidentsForPage(context.Background(), cs, sp, []*storage.Monitor{mon}, time.Now())
	if len(got) != 0 {
		t.Fatalf("expected no incidents, got %d", len(got))
	}
	if n := cs.calls.Load(); n != 1 {
		t.Fatalf("one public page view issued %d incident queries, want 1", n)
	}
}

func TestPublicIncidentsForPageFilters(t *testing.T) {
	cs, mon, raw := incidentStore(t)
	now := time.Now().UTC()
	ins := func(status string, started time.Time, resolved *time.Time) {
		t.Helper()
		var r any
		if resolved != nil {
			r = resolved.Format(tsLayout)
		}
		if _, err := raw.Exec(`INSERT INTO incidents (monitor_id, status, cause, started_at, resolved_at) VALUES (?, ?, 'secret cause', ?, ?)`,
			mon.ID, status, started.Format(tsLayout), r); err != nil {
			t.Fatal(err)
		}
	}
	oldResolved := now.AddDate(0, 0, -20)
	recentResolved := now.AddDate(0, 0, -2)
	ins("resolved", oldResolved.Add(-time.Hour), &oldResolved)       // excluded: resolved before cutoff
	ins("resolved", recentResolved.Add(-time.Hour), &recentResolved) // included
	ins("open", now.AddDate(0, 0, -30), nil)                         // included: still open

	sp := &storage.StatusPage{ShowIncidents: true}
	got := PublicIncidentsForPage(context.Background(), cs, sp, []*storage.Monitor{mon}, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 incidents, got %d", len(got))
	}
	if got[0].Status != "resolved" || got[1].Status != "open" {
		t.Fatalf("expected newest first (resolved, open), got (%s, %s)", got[0].Status, got[1].Status)
	}
	for _, inc := range got {
		if inc.MonitorID != mon.ID {
			t.Fatalf("incident of monitor %d leaked onto the page", inc.MonitorID)
		}
		if inc.Cause != "" {
			t.Fatalf("cause %q exposed on public page", inc.Cause)
		}
	}

	for i := 0; i < 15; i++ {
		ins("open", now.Add(-time.Duration(i)*time.Minute), nil)
	}
	if got := PublicIncidentsForPage(context.Background(), cs, sp, []*storage.Monitor{mon}, now); len(got) != 10 {
		t.Fatalf("expected at most 10 incidents, got %d", len(got))
	}
	if got := PublicIncidentsForPage(context.Background(), cs, &storage.StatusPage{}, []*storage.Monitor{mon}, now); len(got) != 0 {
		t.Fatal("incidents returned with ShowIncidents=false")
	}
}

func BenchmarkPublicIncidentsForPage(b *testing.B) {
	cs, mon, _ := incidentStore(b)
	sp := &storage.StatusPage{ShowIncidents: true}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PublicIncidentsForPage(ctx, cs, sp, []*storage.Monitor{mon}, time.Now())
	}
}

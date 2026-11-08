package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestPublicStatusPageIncidentSelectionIsBoundedAndPrivate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	monitor, other := newTestMonitor(t, s, "page"), newTestMonitor(t, s, "other")
	page := &StatusPage{Slug: "incidents", Enabled: true, ShowIncidents: true}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatusPageMonitors(ctx, page.ID, []StatusPageMonitor{{MonitorID: monitor.ID}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old, recent := now.AddDate(0, 0, -20), now.AddDate(0, 0, -2)
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO incidents(monitor_id,status,cause,started_at,resolved_at) VALUES (?,?,'private cause',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := range 5000 {
		ts := old.Add(time.Duration(i) * time.Minute)
		if _, err := stmt.ExecContext(ctx, other.ID, "resolved", formatTime(ts), formatTime(ts)); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		status   string
		start    time.Time
		resolved any
	}{
		{"resolved", old.Add(-time.Hour), formatTime(old)},
		{"resolved", recent.Add(-time.Hour), formatTime(recent)},
		{"open", now.AddDate(0, 0, -30), nil},
	} {
		if _, err := stmt.ExecContext(ctx, monitor.ID, row.status, formatTime(row.start), row.resolved); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	readerTx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	reader := &publicPageReader{monitorReadQuery: readerTx}
	got, err := publicStatusPageSnapshotQuery(ctx, reader, s.readDB.dialect, page.ID, PublicStatusPageAccess{Audience: PublicStatusPageWeb}, now, false)
	readerTx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Incidents) != 2 || got.Incidents[0].Status != "resolved" || got.Incidents[1].Status != "open" {
		t.Fatalf("incorrect recent/open incident selection: %+v", got.Incidents)
	}
	queries := 0
	for _, query := range reader.queries {
		if strings.Contains(query, "FROM incidents") {
			queries++
		}
	}
	if queries != 1 {
		t.Fatalf("public page issued %d incident queries", queries)
	}
	for _, incident := range got.Incidents {
		if incident.MonitorID != monitor.ID || incident.Cause != "" {
			t.Fatal("public incident projection leaked another monitor or private diagnostic")
		}
	}
	for i := range 15 {
		if _, err := s.writeDB.Exec(`INSERT INTO incidents(monitor_id,status,started_at) VALUES (?,'open',?)`, monitor.ID, formatTime(now.Add(-time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.GetPublicStatusPageSnapshot(ctx, page.ID, PublicStatusPageAccess{Audience: PublicStatusPageWeb}, now, false)
	if err != nil || len(got.Incidents) != 10 {
		t.Fatalf("incident result not bounded: %+v %v", got, err)
	}
	if _, err := s.writeDB.Exec("UPDATE status_pages SET show_incidents=0 WHERE id=?", page.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetPublicStatusPageSnapshot(ctx, page.ID, PublicStatusPageAccess{Audience: PublicStatusPageWeb}, now, false)
	if err != nil || len(got.Incidents) != 0 {
		t.Fatalf("hidden incidents returned: %+v %v", got, err)
	}
}

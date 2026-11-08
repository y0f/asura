package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

type publicPageReader struct {
	monitorReadQuery
	beforeMembers func()
	queries       []string
	failQuery     string
}

func (r *publicPageReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.queries = append(r.queries, query)
	if r.failQuery != "" && strings.Contains(query, r.failQuery) {
		return nil, errors.New("injected public snapshot read failure")
	}
	if strings.Contains(query, "FROM status_page_monitors") && r.beforeMembers != nil {
		callback := r.beforeMembers
		r.beforeMembers = nil
		callback()
	}
	return r.monitorReadQuery.QueryContext(ctx, query, args...)
}

func TestPublicStatusPageSnapshotKeepsPublicationAndDataTogether(t *testing.T) {
	for _, uptime := range []bool{false, true} {
		t.Run(map[bool]string{false: "RSS", true: "uptime"}[uptime], func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			old := newTestMonitor(t, s, "published")
			private := newTestMonitor(t, s, "private")
			page := &StatusPage{Slug: "public", Title: "Public title", Enabled: true, ShowIncidents: true}
			if err := s.CreateStatusPage(ctx, page); err != nil {
				t.Fatal(err)
			}
			if err := s.SetStatusPageMonitors(ctx, page.ID, []StatusPageMonitor{{MonitorID: old.ID, GroupName: "Public group"}}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			if _, err := s.writeDB.Exec(`INSERT INTO check_results(monitor_id,status,response_time,created_at,observation_kind) VALUES (?,'up',0,?,'probe')`, old.ID, formatTime(now.Add(-time.Minute))); err != nil {
				t.Fatal(err)
			}
			incident := &Incident{MonitorID: old.ID, Status: "open", Cause: "private diagnostic"}
			if err := s.CreateIncident(ctx, incident); err != nil {
				t.Fatal(err)
			}
			if _, err := s.writeDB.Exec("UPDATE incidents SET acknowledged_by='private account' WHERE id=?", incident.ID); err != nil {
				t.Fatal(err)
			}
			tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			reader := &publicPageReader{monitorReadQuery: tx, beforeMembers: func() {
				_, err := s.SaveStatusPage(ctx, page.ID, func(current *StatusPage) (*StatusPage, *[]StatusPageMonitor, error) {
					next := *current
					next.PasswordHash, next.Title = "new-private-hash", "Private title"
					members := []StatusPageMonitor{{MonitorID: private.ID}}
					return &next, &members, nil
				}, StatusPageAccess{Monitors: []MonitorAccess{{All: true}}, Incidents: []MonitorAccess{{All: true}}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.writeDB.Exec(`INSERT INTO check_results(monitor_id,status,response_time,created_at,observation_kind) VALUES (?,'down',0,?,'probe')`, old.ID, formatTime(now.Add(-time.Second))); err != nil {
					t.Fatal(err)
				}
			}}
			access := PublicStatusPageAccess{Audience: PublicStatusPageWeb}
			snapshot, err := publicStatusPageSnapshotQuery(ctx, reader, s.readDB.dialect, page.ID, access, now, uptime)
			if err != nil {
				t.Fatal(err)
			}
			if reader.beforeMembers != nil || snapshot.Page.Title != "Public title" || len(snapshot.Monitors) != 1 || snapshot.Monitors[0].ID != old.ID || snapshot.Members[0].GroupName != "Public group" {
				t.Fatalf("mixed publication and membership: %+v", snapshot)
			}
			if snapshot.Monitors[0].Target != "" || len(snapshot.Monitors[0].Settings) != 0 || len(snapshot.Incidents) != 1 || snapshot.Incidents[0].Cause != "" || snapshot.Incidents[0].AcknowledgedBy != "" {
				t.Fatal("public snapshot loaded private monitor or incident fields")
			}
			if uptime {
				percent, count := SummarizeUptime(snapshot.DailyUptime[old.ID])
				if percent != 100 || count != 1 {
					t.Fatalf("mixed authorization and new observations: %.1f/%d", percent, count)
				}
			} else {
				for _, query := range reader.queries {
					if strings.Contains(query, "check_result") {
						t.Fatal("RSS snapshot read unused metrics")
					}
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetPublicStatusPageSnapshot(ctx, page.ID, access, now, uptime); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("old public authorization admitted newly private data: %v", err)
			}
		})
	}
}

func TestPublicStatusPageSnapshotRequiresCurrentHashAndVisibility(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	page := &StatusPage{Slug: "visibility", Title: "Visibility", PasswordHash: "private-hash"}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		for _, apiEnabled := range []bool{false, true} {
			if _, err := s.writeDB.Exec("UPDATE status_pages SET enabled=?,api_enabled=? WHERE id=?", boolToInt(enabled), boolToInt(apiEnabled), page.ID); err != nil {
				t.Fatal(err)
			}
			for _, audience := range []PublicStatusPageAudience{0, PublicStatusPageWeb, PublicStatusPageJSON, PublicStatusPageWebSubscription, PublicStatusPageAPISubscription} {
				for _, hash := range []string{"", "wrong", page.PasswordHash} {
					allowed := hash == page.PasswordHash && (audience == PublicStatusPageWeb && enabled || audience == PublicStatusPageWebSubscription && enabled || audience == PublicStatusPageJSON && (enabled || apiEnabled) || audience == PublicStatusPageAPISubscription && enabled && apiEnabled)
					_, err := s.GetPublicStatusPageSnapshot(ctx, page.ID, PublicStatusPageAccess{ExpectedPasswordHash: hash, Audience: audience}, time.Now(), false)
					if allowed && err != nil || !allowed && !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("visibility=%v/%v audience=%d hashmatch=%v allowed=%v err=%v", enabled, apiEnabled, audience, hash == page.PasswordHash, allowed, err)
					}
				}
			}
		}
	}
}

func TestPublicStatusPageSnapshotNeverReturnsPartialReadFailures(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	monitor := newTestMonitor(t, s, "published")
	page := &StatusPage{Slug: "failure", Enabled: true, ShowIncidents: true}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatusPageMonitors(ctx, page.ID, []StatusPageMonitor{{MonitorID: monitor.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"FROM status_page_monitors", "FROM check_result_daily", "FROM incidents"} {
		t.Run(query, func(t *testing.T) {
			tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			reader := &publicPageReader{monitorReadQuery: tx, failQuery: query}
			got, err := publicStatusPageSnapshotQuery(ctx, reader, s.readDB.dialect, page.ID, PublicStatusPageAccess{Audience: PublicStatusPageWeb}, time.Now(), true)
			if err == nil || got != nil {
				t.Fatalf("partial snapshot returned after failed %s: %+v %v", query, got, err)
			}
		})
	}
}

func TestPublicSubscriptionFailureDoesNotExposeUncommittedEnrollment(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	page := &StatusPage{Slug: "admission-failure", Enabled: true}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	query := `CREATE TRIGGER reject_public_subscriber AFTER INSERT ON status_page_subscribers BEGIN SELECT RAISE(ABORT,'injected subscriber failure'); END`
	if s.writeDB.dialect == postgresDialect {
		query = `CREATE FUNCTION reject_public_subscriber() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected subscriber failure'; END $$;
		CREATE TRIGGER reject_public_subscriber AFTER INSERT ON status_page_subscribers FOR EACH ROW EXECUTE FUNCTION reject_public_subscriber()`
	}
	if _, err := s.writeDB.Exec(query); err != nil {
		t.Fatal(err)
	}
	sub := &StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: "fixture@example.test"}
	got, err := s.CreatePublicStatusPageSubscriber(ctx, sub, PublicStatusPageAccess{Audience: PublicStatusPageWebSubscription})
	if err == nil || got != nil || sub.ID != 0 || sub.Token != "" {
		t.Fatal("failed transaction exposed an enrollment or successful page result")
	}
	var count int
	if err := s.readDB.QueryRow("SELECT COUNT(*) FROM status_page_subscribers").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed subscriber transaction persisted data: %d %v", count, err)
	}
}

func TestPublicSubscriptionRechecksAuthorityAfterWaitingForWriter(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	page := &StatusPage{Slug: "subscribe", Title: "Subscribe", Enabled: true, APIEnabled: true}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sub := &StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: "fixture@example.test"}
	beforeWait := s.writeDB.Stats().WaitCount
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := s.CreatePublicStatusPageSubscriber(ctx, sub, PublicStatusPageAccess{Audience: PublicStatusPageAPISubscription})
		done <- err
	}()
	defer func() { cancel(); tx.Rollback(); <-finished }()
	deadline := time.Now().Add(2 * time.Second)
	for s.writeDB.Stats().WaitCount == beforeWait {
		if time.Now().After(deadline) {
			t.Fatal("subscriber did not reach the writer wait boundary")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE status_pages SET password_hash='new-private-hash' WHERE id=?", page.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, sql.ErrNoRows) || sub.ID != 0 || sub.Token != "" {
		t.Fatalf("old public admission created a subscriber: id=%d tokenSet=%v err=%v", sub.ID, sub.Token != "", err)
	}
	var count int
	if err := s.readDB.QueryRow("SELECT COUNT(*) FROM status_page_subscribers").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected admission persisted a row: count=%d err=%v", count, err)
	}
	gotPage, err := s.CreatePublicStatusPageSubscriber(ctx, sub, PublicStatusPageAccess{Audience: PublicStatusPageAPISubscription, ExpectedPasswordHash: "new-private-hash"})
	if err != nil || gotPage == nil || sub.ID == 0 || sub.Token == "" {
		t.Fatalf("current authorization could not subscribe: %v", err)
	}
}

func TestPublicSubscriptionTokenMutationsBindPageAndAllowDisabledUnsubscribe(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	page := &StatusPage{Slug: "tokens", Enabled: true}
	if err := s.CreateStatusPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	sub := &StatusPageSubscriber{StatusPageID: page.ID, Type: "email", Email: "fixture@example.test"}
	if err := s.CreateStatusPageSubscriber(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmPageSubscriber(ctx, page.ID+1, sub.Token); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("token confirmed through another page: %v", err)
	}
	if err := s.DeletePageSubscriberByToken(ctx, page.ID+1, sub.Token); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("token deleted through another page: %v", err)
	}
	for range 2 {
		if err := s.ConfirmPageSubscriber(ctx, page.ID, sub.Token); err != nil {
			t.Fatalf("confirmation is not idempotent: %v", err)
		}
	}
	if _, err := s.writeDB.Exec("UPDATE status_pages SET enabled=0 WHERE id=?", page.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmPageSubscriber(ctx, page.ID, sub.Token); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("disabled page admitted confirmation: %v", err)
	}
	if err := s.DeletePageSubscriberByToken(ctx, page.ID, sub.Token); err != nil {
		t.Fatalf("disabled page prevented unsubscribe: %v", err)
	}
	if _, err := s.GetSubscriberByToken(ctx, sub.Token); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unsubscribe did not delete the subscriber: %v", err)
	}
}

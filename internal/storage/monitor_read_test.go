package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestMonitorOwnedReadsCheckCurrentScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first, second := &MonitorGroup{Name: "first"}, &MonitorGroup{Name: "second"}
	if err := s.CreateMonitorGroup(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMonitorGroup(ctx, second); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{Name: "monitor", Type: "http", Target: "https://example.test", GroupID: &first.ID, Enabled: true, Interval: 60, Timeout: 10}
	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCheckResult(ctx, &CheckResult{MonitorID: m.ID, Status: "up", ResponseTime: 25, Message: "private check"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertContentChange(ctx, &ContentChange{MonitorID: m.ID, OldHash: "old", NewHash: "new", Diff: "private change"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateIncident(ctx, &Incident{MonitorID: m.ID, Status: "open", Cause: "private incident"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHeartbeat(ctx, &Heartbeat{MonitorID: m.ID, Token: "fixture-token", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	tag := &Tag{Name: "private-tag", Color: "#005bbf"}
	if err := s.CreateTag(ctx, tag); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMonitorTags(ctx, m.ID, []MonitorTag{{TagID: tag.ID, Value: "private-value"}}); err != nil {
		t.Fatal(err)
	}
	before, after := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	page := Pagination{Page: 1, PerPage: 10}
	operations := []struct {
		name string
		read func(...MonitorAccess) error
	}{
		{"checks", func(a ...MonitorAccess) error { _, e := s.ListCheckResults(ctx, m.ID, page, a...); return e }},
		{"latest", func(a ...MonitorAccess) error { _, e := s.GetLatestCheckResult(ctx, m.ID, a...); return e }},
		{"changes", func(a ...MonitorAccess) error { _, e := s.ListContentChanges(ctx, m.ID, page, a...); return e }},
		{"channels", func(a ...MonitorAccess) error { _, e := s.GetMonitorNotificationChannelIDs(ctx, m.ID, a...); return e }},
		{"agents", func(a ...MonitorAccess) error { _, e := s.GetMonitorAgentIDs(ctx, m.ID, a...); return e }},
		{"tags", func(a ...MonitorAccess) error { _, e := s.GetMonitorTags(ctx, m.ID, a...); return e }},
		{"heartbeat", func(a ...MonitorAccess) error { _, e := s.GetHeartbeatByMonitorID(ctx, m.ID, a...); return e }},
		{"incident", func(a ...MonitorAccess) error { _, e := s.GetOpenIncident(ctx, m.ID, a...); return e }},
		{"daily uptime", func(a ...MonitorAccess) error { _, e := s.GetDailyUptime(ctx, m.ID, before, after, a...); return e }},
		{"uptime percent", func(a ...MonitorAccess) error { _, e := s.GetUptimePercent(ctx, m.ID, before, after, a...); return e }},
		{"percentiles", func(a ...MonitorAccess) error {
			_, _, _, e := s.GetResponseTimePercentiles(ctx, m.ID, before, after, a...)
			return e
		}},
		{"counts", func(a ...MonitorAccess) error {
			_, _, _, _, e := s.GetCheckCounts(ctx, m.ID, before, after, a...)
			return e
		}},
		{"raw chart", func(a ...MonitorAccess) error {
			_, e := s.GetResponseTimeSeries(ctx, m.ID, before, after, 50, a...)
			return e
		}},
		{"hourly chart", func(a ...MonitorAccess) error {
			_, e := s.GetResponseTimeSeries(ctx, m.ID, before.Add(-30*24*time.Hour), after, 50, a...)
			return e
		}},
		{"daily chart", func(a ...MonitorAccess) error {
			_, e := s.GetResponseTimeSeries(ctx, m.ID, before.Add(-180*24*time.Hour), after, 50, a...)
			return e
		}},
	}
	allowed := MonitorAccess{GroupIDs: []int64{first.ID}}
	denied := MonitorAccess{GroupIDs: []int64{second.ID}}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.read(allowed); err != nil {
				t.Fatalf("allowed read: %v", err)
			}
			for _, access := range [][]MonitorAccess{{denied}, {{}}, {allowed, denied}} {
				if err := operation.read(access...); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("denied read returned %v", err)
				}
			}
		})
	}
	m.GroupID = &second.ID
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if err := operation.read(allowed); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("%s used old group authority: %v", operation.name, err)
		}
		if err := operation.read(denied); err != nil {
			t.Errorf("%s denied current group authority: %v", operation.name, err)
		}
	}
	if data, err := s.GetMonitorSparklines(ctx, []int64{m.ID}, 10, allowed); err != nil || len(data) != 0 {
		t.Fatalf("sparkline leaked moved monitor: %+v %v", data, err)
	}
	if data, err := s.GetMonitorTagsBatch(ctx, []int64{m.ID}, allowed); err != nil || len(data) != 0 {
		t.Fatalf("tags leaked moved monitor: %+v %v", data, err)
	}
}

func TestMonitorReadSnapshotCannotMixAuthorizationAndNewData(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	group := &MonitorGroup{Name: "allowed"}
	if err := s.CreateMonitorGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	m := &Monitor{Name: "monitor", Type: "http", Target: "https://example.test", GroupID: &group.ID, Enabled: true, Interval: 60, Timeout: 10}
	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCheckResult(ctx, &CheckResult{MonitorID: m.ID, Status: "up"}); err != nil {
		t.Fatal(err)
	}
	access := MonitorAccess{GroupIDs: []int64{group.ID}}
	reader, release, err := s.monitorReader(ctx, m.ID, access)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Authorization acquired a read snapshot. Later ownership and history
	// changes cannot enter that already-authorized snapshot.
	m.GroupID = nil
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCheckResult(ctx, &CheckResult{MonitorID: m.ID, Status: "down"}); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM check_results WHERE monitor_id=?", m.ID).Scan(&total); err != nil || total != 1 {
		t.Fatalf("snapshot mixed subsequent history: %d %v", total, err)
	}
	release()
	if _, err := s.ListCheckResults(ctx, m.ID, Pagination{Page: 1, PerPage: 10}, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("new read retained revoked group: %v", err)
	}
}

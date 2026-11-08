package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestMonitorCloneUsesCurrentSourceAndSeparateDestination(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	source := monitors[0]
	channel := &NotificationChannel{Name: "current", Type: "webhook", Settings: []byte("{}")}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMonitorNotificationChannels(ctx, source.ID, []int64{channel.ID}); err != nil {
		t.Fatal(err)
	}
	called := false
	prepare := func(current *Monitor) (*Monitor, MonitorCreateOptions, error) {
		called = true
		if !reflect.DeepEqual(current.NotificationChannelIDs, []int64{channel.ID}) {
			t.Fatalf("clone preparation got stale assignments: %+v", current)
		}
		current.ID, current.Name, current.Enabled = 999999, "clone", false
		return current, MonitorCreateOptions{}, nil
	}
	allowedSource := []MonitorAccess{{GroupIDs: []int64{groups[0].ID}}}
	if clone, _, err := store.CloneMonitorWithRelations(ctx, source.ID, prepare, []MonitorAccess{{}}, []MonitorAccess{{All: true}}); clone != nil || !errors.Is(err, sql.ErrNoRows) || called {
		t.Fatalf("inaccessible source reached callback: %+v, %v, %t", clone, err, called)
	}
	if clone, _, err := store.CloneMonitorWithRelations(ctx, source.ID, prepare, allowedSource, []MonitorAccess{{MonitorIDs: []int64{source.ID}}}); clone != nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ID-only destination created another monitor: %+v, %v", clone, err)
	}
	clone, heartbeat, err := store.CloneMonitorWithRelations(ctx, source.ID, prepare, allowedSource, []MonitorAccess{{GroupIDs: []int64{groups[0].ID}}})
	if err != nil || heartbeat != nil || clone.ID == source.ID || clone.ID == 999999 || clone.Enabled || clone.Status != "paused" {
		t.Fatalf("authorized clone failed or retained supplied identity/runtime state: %+v, %+v, %v", clone, heartbeat, err)
	}
	ids, err := store.GetMonitorNotificationChannelIDs(ctx, clone.ID)
	if err != nil || !reflect.DeepEqual(ids, []int64{channel.ID}) {
		t.Fatalf("clone omitted current relationship: %v, %v", ids, err)
	}
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{source.ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	called = false
	if clone, _, err := store.CloneMonitorWithRelations(ctx, source.ID, prepare, allowedSource, []MonitorAccess{{All: true}}); clone != nil || !errors.Is(err, sql.ErrNoRows) || called {
		t.Fatalf("stale source group retained clone authority: %+v, %v, %t", clone, err, called)
	}
	result, err := store.ListMonitors(ctx, MonitorListFilter{}, Pagination{Page: 1, PerPage: 100})
	if err != nil || result.Total != int64(len(monitors)+1) {
		t.Fatalf("denied clone left partial rows: %+v, %v", result, err)
	}
}

func TestMonitorCloneRollsBackCredentialFailure(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	source := &Monitor{Name: "heartbeat", Type: "heartbeat", Target: "heartbeat", Interval: 60, Timeout: 10}
	credential := &Heartbeat{Token: "original-token"}
	if err := store.CreateMonitorWithRelations(ctx, source, MonitorCreateOptions{Heartbeat: credential}); err != nil {
		t.Fatal(err)
	}
	clone, heartbeat, err := store.CloneMonitorWithRelations(ctx, source.ID, func(current *Monitor) (*Monitor, MonitorCreateOptions, error) {
		current.Name = "failed clone"
		return current, MonitorCreateOptions{Heartbeat: &Heartbeat{Token: credential.Token}}, nil
	}, []MonitorAccess{{All: true}}, []MonitorAccess{{All: true}})
	if err == nil || clone != nil || heartbeat != nil {
		t.Fatalf("failed credential creation exposed uncommitted clone: %+v, %+v, %v", clone, heartbeat, err)
	}
	for _, table := range []string{"monitors", "monitor_status", "heartbeats"} {
		var count int
		if err := store.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("failed clone left partial %s rows: %d, %v", table, count, err)
		}
	}
}

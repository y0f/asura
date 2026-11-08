package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestMonitorCreationCommitsHeartbeatCredentialAtomically(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	newMonitor := func() *Monitor {
		return &Monitor{Name: "heartbeat", Type: "heartbeat", Target: "heartbeat", Interval: 60, Timeout: 10, Enabled: true}
	}
	monitor := newMonitor()
	credential := &Heartbeat{Token: "one-time-credential", Grace: 5}
	if err := store.CreateMonitorWithRelations(ctx, monitor, MonitorCreateOptions{Heartbeat: credential}, MonitorAccess{All: true}); err != nil {
		t.Fatal(err)
	}
	if monitor.ID == 0 || credential.ID == 0 || credential.MonitorID != monitor.ID || credential.Token != "one-time-credential" || credential.Status != "pending" {
		t.Fatalf("committed output lost its binding or one-time token: %+v, %+v", monitor, credential)
	}
	stored, err := store.GetHeartbeatByToken(ctx, credential.Token)
	if err != nil || stored.MonitorID != monitor.ID || stored.Token == credential.Token {
		t.Fatalf("credential was not bound and hashed: %+v, %v", stored, err)
	}
	if _, err := store.GetHeartbeatByToken(ctx, stored.Token); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stored hash became a usable credential: %v", err)
	}
	duplicate, duplicateCredential := newMonitor(), &Heartbeat{Token: credential.Token}
	if err := store.CreateMonitorWithRelations(ctx, duplicate, MonitorCreateOptions{Heartbeat: duplicateCredential}, MonitorAccess{All: true}); err == nil {
		t.Fatal("duplicate heartbeat token unexpectedly accepted")
	}
	if duplicate.ID != 0 || duplicate.Settings != nil || duplicateCredential.ID != 0 || duplicateCredential.MonitorID != 0 {
		t.Fatalf("failed create changed caller-owned outputs: %+v, %+v", duplicate, duplicateCredential)
	}
	var monitors, statuses, heartbeats int
	for table, count := range map[string]*int{"monitors": &monitors, "monitor_status": &statuses, "heartbeats": &heartbeats} {
		if err := store.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(count); err != nil {
			t.Fatal(err)
		}
	}
	if monitors != 1 || statuses != 1 || heartbeats != 1 {
		t.Fatalf("credential failure left partial rows: monitors=%d, statuses=%d, heartbeats=%d", monitors, statuses, heartbeats)
	}
}

func TestMonitorCreationRejectsMissingOrMismatchedHeartbeat(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		monitorType string
		heartbeat   *Heartbeat
	}{
		{"heartbeat", nil}, {"heartbeat", &Heartbeat{}}, {"heartbeat", &Heartbeat{Token: "token", MonitorID: 3}},
		{"heartbeat", &Heartbeat{Token: "token", Grace: -1}}, {"http", &Heartbeat{Token: "token"}},
	} {
		m := &Monitor{Name: "invalid", Type: tc.monitorType, Target: "example.test"}
		if err := store.CreateMonitorWithRelations(ctx, m, MonitorCreateOptions{Heartbeat: tc.heartbeat}, MonitorAccess{All: true}); err == nil || m.ID != 0 {
			t.Fatalf("invalid dependent state was accepted: %+v, %+v, %v", m, tc.heartbeat, err)
		}
	}
}

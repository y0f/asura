package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestExplicitAgentAssignmentMigrationPreservesEligibleEncryptedJobs(t *testing.T) {
	ctx := context.Background()
	gcm, err := newGCM(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc := &Encryptor{gcm: gcm, legacyGCM: gcm}
	agents := []*Agent{{Name: "first", Enabled: true}, {Name: "second", Enabled: true}, {Name: "disabled"}}
	monitors := []*Monitor{
		{Name: "plain", Type: "http", Settings: []byte(`{}`)},
		{Name: "encrypted", Type: "http", Settings: []byte(`{"method":"GET"}`)},
		{Name: "headers", Type: "http", Settings: []byte(`{"headers":{"Authorization":"private"}}`)},
		{Name: "typed secret", Type: "http", Settings: []byte(`{"password":"private"}`)},
		{Name: "malformed", Type: "http", Settings: []byte(`[]`)},
		{Name: "explicit headers", Type: "http", Settings: []byte(`{"headers":{"Authorization":"private"}}`)},
		{Name: "explicit typed", Type: "http", Settings: []byte(`{"password":"private"}`)},
		{Name: "paused", Type: "http", Settings: []byte(`{}`)},
	}
	store := reopenAtVersion(t, 38, func(s *SQLiteStore) {
		for _, agent := range agents {
			if err := s.CreateAgent(ctx, agent); err != nil {
				t.Fatal(err)
			}
		}
		for i, m := range monitors {
			if i == 1 {
				s.SetEncryptor(enc)
			}
			m.Target, m.Interval, m.Timeout, m.AgentEnabled, m.Enabled = "https://example.test", 60, 10, true, i != 7
			if err := s.CreateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			if i == 5 || i == 6 {
				if err := s.SetMonitorAgentIDs(ctx, m.ID, []int64{agents[0].ID}); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	// A late enrollment and a previously disabled agent cannot join the saved
	// list of delegates, even if enabled before finalization.
	late := &Agent{Name: "late", Enabled: true}
	if err := store.CreateAgent(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.ExecContext(ctx, "UPDATE agents SET enabled=1 WHERE id=?", agents[2].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeLegacyAgentAssignments(ctx); err == nil {
		t.Fatal("encrypted migration unexpectedly completed without its key")
	}
	if ids, err := store.GetMonitorAgentIDs(ctx, monitors[0].ID); err != nil || len(ids) != 0 {
		t.Fatalf("decryption failure committed an earlier plaintext assignment: %v, %v", ids, err)
	}
	wrongGCM, err := newGCM([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetEncryptor(&Encryptor{gcm: wrongGCM, legacyGCM: wrongGCM})
	if err := store.FinalizeLegacyAgentAssignments(ctx); err == nil {
		t.Fatal("migration accepted a wrong encryption key")
	}
	if ids, err := store.GetMonitorAgentIDs(ctx, monitors[0].ID); err != nil || len(ids) != 0 {
		t.Fatalf("wrong-key failure committed earlier assignments: %v, %v", ids, err)
	}
	store.SetEncryptor(enc)
	if err := store.FinalizeLegacyAgentAssignments(ctx); err != nil {
		t.Fatal(err)
	}
	for i, m := range monitors {
		want := []int64{}
		if i == 0 || i == 1 || i == 7 {
			want = []int64{agents[0].ID, agents[1].ID}
		}
		if i == 5 || i == 6 {
			want = []int64{agents[0].ID}
		}
		ids, err := store.GetMonitorAgentIDs(ctx, m.ID)
		if err != nil || !reflect.DeepEqual(ids, want) {
			t.Errorf("%s delegates=%v, want %v, error=%v", m.Name, ids, want, err)
		}
	}
	for _, agent := range []*Agent{agents[2], late} {
		jobs, err := store.ListAgentJobs(ctx, agent.ID)
		if err != nil || len(jobs) != 0 {
			t.Fatalf("unenrolled legacy delegate received jobs: %+v, %v", jobs, err)
		}
	}
	if allowed, err := store.AgentMayCheckMonitor(ctx, agents[0].ID, monitors[6].ID); err != nil || allowed {
		t.Fatalf("explicit assignment bypassed typed-secret server-only rule: %t, %v", allowed, err)
	}
	newMonitor := &Monitor{Name: "new", Type: "http", Target: "https://example.test", Enabled: true, AgentEnabled: true, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, newMonitor); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeLegacyAgentAssignments(ctx); err != nil {
		t.Fatal(err)
	}
	if allowed, err := store.AgentMayCheckMonitor(ctx, agents[0].ID, newMonitor.ID); err != nil || allowed {
		t.Fatalf("repeated migration implicitly delegated a new monitor: %t, %v", allowed, err)
	}
	var pending int
	if err := store.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM legacy_agent_assignments").Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("migration left pending IDs: %d, %v", pending, err)
	}
}

func TestExplicitAgentAssignmentMigrationRollsBackDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	var monitors []*Monitor
	var agentID int64
	store := reopenAtVersion(t, 38, func(s *SQLiteStore) {
		agent := &Agent{Name: "delegate", Enabled: true}
		if err := s.CreateAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
		agentID = agent.ID
		for i := range 2 {
			m := &Monitor{Name: fmt.Sprint(i), Type: "http", Target: "https://example.test", Enabled: true, AgentEnabled: true, Interval: 60, Timeout: 10}
			if err := s.CreateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			monitors = append(monitors, m)
		}
	})
	if _, err := store.writeDB.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER fail_legacy_assignment BEFORE INSERT ON monitor_agents WHEN NEW.monitor_id=%d BEGIN SELECT RAISE(ABORT,'injected assignment failure'); END`, monitors[1].ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeLegacyAgentAssignments(ctx); err == nil {
		t.Fatal("expected injected assignment failure")
	}
	for _, m := range monitors {
		if ids, err := store.GetMonitorAgentIDs(ctx, m.ID); err != nil || len(ids) != 0 {
			t.Fatalf("failed migration partially assigned %d: %v, %v", m.ID, ids, err)
		}
	}
	var pending int
	if err := store.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM legacy_agent_assignments").Scan(&pending); err != nil || pending != 3 {
		t.Fatalf("failed migration lost retry state: %d, %v", pending, err)
	}
	if _, err := store.writeDB.ExecContext(ctx, "DROP TRIGGER fail_legacy_assignment"); err != nil {
		t.Fatal(err)
	}
	// An agent disabled after the snapshot is also excluded at finalization.
	if _, err := store.writeDB.ExecContext(ctx, "UPDATE agents SET enabled=0 WHERE id=?", agentID); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeLegacyAgentAssignments(ctx); err != nil {
		t.Fatal(err)
	}
	for _, m := range monitors {
		if ids, err := store.GetMonitorAgentIDs(ctx, m.ID); err != nil || len(ids) != 0 {
			t.Fatalf("disabled agent received assignment: %v, %v", ids, err)
		}
	}
}

func TestExplicitAgentAssignmentMigrationUsesSnapshotAndCurrentState(t *testing.T) {
	ctx := context.Background()
	var monitors []*Monitor
	var original *Agent
	store := reopenAtVersion(t, 38, func(s *SQLiteStore) {
		original = &Agent{Name: "original", Enabled: true}
		if err := s.CreateAgent(ctx, original); err != nil {
			t.Fatal(err)
		}
		// More than one batch, with ineligible rows between eligible candidates.
		for i := range 40 {
			settings := []byte(`{}`)
			if i%2 != 0 {
				settings = []byte(`{"headers":{"Authorization":"private"}}`)
			}
			m := &Monitor{Name: fmt.Sprint(i), Type: "http", Target: "https://example.test", Settings: settings, Enabled: true, AgentEnabled: true, Interval: 60, Timeout: 10}
			if err := s.CreateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			monitors = append(monitors, m)
		}
	})
	late := &Agent{Name: "late", Enabled: true}
	if err := store.CreateAgent(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMonitorAgentIDs(ctx, monitors[0].ID, []int64{late.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.ExecContext(ctx, "UPDATE monitors SET agent_enabled=0 WHERE id=?", monitors[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.ExecContext(ctx, "UPDATE monitors SET type='manual' WHERE id=?", monitors[4].ID); err != nil {
		t.Fatal(err)
	}
	newMonitor := &Monitor{Name: "new", Type: "http", Target: "https://example.test", Enabled: true, AgentEnabled: true, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, newMonitor); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeLegacyAgentAssignments(ctx); err != nil {
		t.Fatal(err)
	}
	for i, m := range append(monitors, newMonitor) {
		want := []int64{}
		if i == 0 {
			want = []int64{late.ID}
		} else if i < len(monitors) && i%2 == 0 && i != 2 && i != 4 {
			want = []int64{original.ID}
		}
		ids, err := store.GetMonitorAgentIDs(ctx, m.ID)
		if err != nil || !reflect.DeepEqual(ids, want) {
			t.Fatalf("monitor %d delegates=%v, want %v, error=%v", i, ids, want, err)
		}
	}
}

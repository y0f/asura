package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMonitorRevisionInvalidatesQueuedChecks(t *testing.T) {
	tests := []struct {
		name string
		edit func(context.Context, *SQLiteStore, *Monitor, *Agent, *Proxy) error
	}{
		{"edit", func(ctx context.Context, s *SQLiteStore, m *Monitor, _ *Agent, _ *Proxy) error {
			m.Target += "/edited"
			return s.UpdateMonitor(ctx, m)
		}},
		{"pause resume", func(ctx context.Context, s *SQLiteStore, m *Monitor, _ *Agent, _ *Proxy) error {
			if err := s.SetMonitorEnabled(ctx, m.ID, false); err != nil {
				return err
			}
			return s.SetMonitorEnabled(ctx, m.ID, true)
		}},
		{"bulk group", func(ctx context.Context, s *SQLiteStore, m *Monitor, _ *Agent, _ *Proxy) error {
			_, err := s.BulkSetMonitorGroup(ctx, []int64{m.ID}, nil)
			return err
		}},
		{"delete group", func(ctx context.Context, s *SQLiteStore, m *Monitor, _ *Agent, _ *Proxy) error {
			return s.DeleteMonitorGroup(ctx, *m.GroupID)
		}},
		{"assignments", func(ctx context.Context, s *SQLiteStore, m *Monitor, a *Agent, _ *Proxy) error {
			if err := s.SetMonitorAgentIDs(ctx, m.ID, nil); err != nil {
				return err
			}
			return s.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID})
		}},
		{"agent deleted", func(ctx context.Context, s *SQLiteStore, _ *Monitor, a *Agent, _ *Proxy) error {
			_, err := s.DeleteAgent(ctx, a.ID)
			return err
		}},
		{"agent disabled enabled", func(ctx context.Context, s *SQLiteStore, _ *Monitor, a *Agent, _ *Proxy) error {
			_, err := s.writeDB.ExecContext(ctx, "UPDATE agents SET enabled=0 WHERE id=?; UPDATE agents SET enabled=1 WHERE id=?", a.ID, a.ID)
			return err
		}},
		{"proxy changed", func(ctx context.Context, s *SQLiteStore, _ *Monitor, _ *Agent, p *Proxy) error {
			p.Host = "new.example"
			return s.UpdateProxy(ctx, p)
		}},
		{"proxy deleted", func(ctx context.Context, s *SQLiteStore, _ *Monitor, _ *Agent, p *Proxy) error {
			return s.DeleteProxy(ctx, p.ID)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			a := &Agent{Name: "delegated", Enabled: true}
			if err := s.CreateAgent(ctx, a); err != nil {
				t.Fatal(err)
			}
			p := &Proxy{Name: "proxy", Protocol: "http", Host: "proxy.example", Port: 8080, Enabled: true}
			if err := s.CreateProxy(ctx, p); err != nil {
				t.Fatal(err)
			}
			g := &MonitorGroup{Name: "group"}
			if err := s.CreateMonitorGroup(ctx, g); err != nil {
				t.Fatal(err)
			}
			m := newTestMonitor(t, s, "revision")
			m.ProxyID, m.GroupID, m.AgentEnabled = &p.ID, &g.ID, true
			if err := s.UpdateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			if err := s.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID}); err != nil {
				t.Fatal(err)
			}
			m, err := s.GetMonitor(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := *m
			if err := tt.edit(ctx, s, m, a, p); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetMonitor(ctx, m.ID)
			if err != nil || current.ConfigRevision <= expected.ConfigRevision {
				t.Fatalf("revision did not advance: current=%+v old=%d err=%v", current, expected.ConfigRevision, err)
			}
			for _, agentID := range []*int64{nil, &a.ID} {
				result := &CheckResult{MonitorID: m.ID, Status: "down", AgentID: agentID}
				if err := s.RecordMonitorCheck(ctx, &expected, result, nil); !errors.Is(err, ErrStaleMonitorCheck) {
					t.Fatalf("stale check accepted: %v", err)
				}
				if result.ID != 0 || !result.CreatedAt.IsZero() {
					t.Fatalf("rejected result mutated: %+v", result)
				}
			}
			if _, err := s.GetLatestCheckResult(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("rejected check entered history: %v", err)
			}
		})
	}
}

func TestMonitorCheckRollsBackHistoryAndStatus(t *testing.T) {
	for _, table := range []string{"check_results", "monitor_status"} {
		t.Run(table, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			m := newTestMonitor(t, s, "atomic check")
			before, err := s.GetMonitorStatus(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.writeDB.ExecContext(ctx, "CREATE TRIGGER reject_check BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT, 'injected'); END"); err != nil {
				t.Fatal(err)
			}
			result := &CheckResult{MonitorID: m.ID, Status: "down", CreatedAt: time.Unix(10, 0)}
			status := &MonitorStatus{MonitorID: m.ID, Status: "down", ConsecFails: 1}
			resultBefore, statusBefore := *result, *status
			if err := s.RecordMonitorCheck(ctx, m, result, status); err == nil {
				t.Fatal("write failure ignored")
			}
			if !reflect.DeepEqual(*result, resultBefore) || !reflect.DeepEqual(*status, statusBefore) {
				t.Fatal("failed transaction mutated caller outputs")
			}
			after, err := s.GetMonitorStatus(ctx, m.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("status partly committed: %+v %v", after, err)
			}
			if _, err := s.GetLatestCheckResult(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("history partly committed: %v", err)
			}
			if _, err := s.writeDB.ExecContext(ctx, "DROP TRIGGER reject_check"); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordMonitorCheck(ctx, m, result, status); err != nil {
				t.Fatal(err)
			}
			if result.ID == 0 || status.ConfigRevision != m.ConfigRevision {
				t.Fatalf("missing committed outputs: %+v %+v", result, status)
			}
		})
	}
}

func TestMonitorCheckRequiresCurrentDelegation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := newTestMonitor(t, s, "delegation")
	m.AgentEnabled = true
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Name: "agent", Enabled: true}
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	result := &CheckResult{MonitorID: m.ID, Status: "up", AgentID: &a.ID}
	if err := s.RecordMonitorCheck(ctx, m, result, nil); !errors.Is(err, ErrStaleMonitorCheck) {
		t.Fatalf("unassigned agent accepted: %v", err)
	}
	if err := s.SetMonitorAgentIDs(ctx, m.ID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMonitorCheck(ctx, m, result, nil); err != nil {
		t.Fatalf("assigned observation rejected: %v", err)
	}
	status, err := s.GetMonitorStatus(ctx, m.ID)
	if err != nil || status.Status != "pending" {
		t.Fatalf("history-only observation applied state: %+v %v", status, err)
	}
	m.Settings = []byte(`{"bearer_token":"server-only"}`)
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMonitorCheck(ctx, m, result, nil); !errors.Is(err, ErrStaleMonitorCheck) {
		t.Fatalf("server-only settings accepted: %v", err)
	}
}

func TestUpgradeFromV39AddsMonitorRevision(t *testing.T) {
	var id int64
	s := reopenAtVersion(t, 39, func(s *SQLiteStore) {
		id = newTestMonitor(t, s, "legacy revision").ID
		if err := s.UpsertMonitorStatus(context.Background(), &MonitorStatus{MonitorID: id, Status: "down", ConsecFails: 4}); err != nil {
			t.Fatal(err)
		}
	})
	ctx := context.Background()
	m, err := s.GetMonitor(ctx, id)
	if err != nil || m.ConfigRevision != 1 {
		t.Fatalf("upgrade revision: %+v %v", m, err)
	}
	status, err := s.GetMonitorStatus(ctx, id)
	if err != nil || status.ConfigRevision != m.ConfigRevision || status.ConsecFails != 4 {
		t.Fatalf("legacy streak lost its configuration identity: %+v %v", status, err)
	}
	m.Name = "edited"
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	if m.ConfigRevision != 2 {
		t.Fatalf("upgrade trigger missing: %+v", m)
	}
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAgentProxyAssignmentRemainsLocal(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled proxy", true: "enabled proxy"}[enabled], func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			m, a, p := agentProxyFixture(t, s)
			p.Enabled = enabled
			if err := s.UpdateProxy(ctx, p); err != nil {
				t.Fatal(err)
			}
			m.ProxyID = &p.ID
			if err := s.UpdateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			before, err := s.GetMonitorStatus(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := s.ListAgentJobs(ctx, a.ID)
			if err != nil || len(jobs) != 0 {
				t.Fatalf("proxy monitor offered remotely: %d jobs, %v", len(jobs), err)
			}
			if allowed, err := s.AgentMayCheckMonitor(ctx, a.ID, m.ID); err != nil || allowed {
				t.Fatalf("proxy monitor delegated: %v, %v", allowed, err)
			}
			for _, status := range []*MonitorStatus{nil, {MonitorID: m.ID, Status: "down", ConsecFails: 1}} {
				result := &CheckResult{MonitorID: m.ID, AgentID: &a.ID, Status: "down"}
				if err := s.RecordMonitorCheck(ctx, m, result, status); !errors.Is(err, ErrStaleMonitorCheck) || result.ID != 0 || !result.CreatedAt.IsZero() {
					t.Fatalf("current-revision result bypassed local proxy: %v", err)
				}
			}
			after, err := s.GetMonitorStatus(ctx, m.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected result changed state: %v", err)
			}
			if _, err := s.GetLatestCheckResult(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("rejected result entered history: %v", err)
			}
			// Local checks still execute the configured proxy and can persist.
			local := &CheckResult{MonitorID: m.ID, Status: "down"}
			if err := s.RecordMonitorCheck(ctx, m, local, nil); err != nil {
				t.Fatalf("proxy restriction blocked local result: %v", err)
			}
			m.ProxyID = nil
			if err := s.UpdateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			jobs, err = s.ListAgentJobs(ctx, a.ID)
			if err != nil || len(jobs) != 1 || jobs[0].ConfigRevision != m.ConfigRevision {
				t.Fatalf("removing proxy did not restore explicit delegation: %d jobs, %v", len(jobs), err)
			}
			if allowed, err := s.AgentMayCheckMonitor(ctx, a.ID, m.ID); err != nil || !allowed {
				t.Fatalf("ordinary explicit delegation rejected: %v, %v", allowed, err)
			}
			remote := &CheckResult{MonitorID: m.ID, AgentID: &a.ID, Status: "up"}
			if err := s.RecordMonitorCheck(ctx, m, remote, nil); err != nil {
				t.Fatalf("fresh ordinary agent result rejected: %v", err)
			}
		})
	}
}

func TestAgentProxyChangeRejectsHeldResult(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, a, p := agentProxyFixture(t, s)
	jobs, err := s.ListAgentJobs(ctx, a.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("initial job: %d, %v", len(jobs), err)
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE monitors SET proxy_id=? WHERE id=?", p.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	beforeWait := s.writeDB.Stats().WaitCount
	result := &CheckResult{MonitorID: m.ID, AgentID: &a.ID, Status: "down"}
	done := make(chan error, 1)
	go func() { done <- s.RecordMonitorCheck(ctx, m, result, nil) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for s.writeDB.Stats().WaitCount == beforeWait {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("result did not wait for current proxy assignment")
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrStaleMonitorCheck) || result.ID != 0 {
		t.Fatalf("queued result survived proxy change: %v", err)
	}
	current, err := s.GetMonitor(ctx, m.ID)
	if err != nil || current.ConfigRevision <= jobs[0].ConfigRevision {
		t.Fatalf("proxy change did not invalidate job revision: %v", err)
	}
	if _, err := s.GetLatestCheckResult(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("queued proxy-bypassing result entered history: %v", err)
	}
}

func agentProxyFixture(t *testing.T, s *SQLiteStore) (*Monitor, *Agent, *Proxy) {
	t.Helper()
	ctx := context.Background()
	a := &Agent{Name: "explicit delegate", Enabled: true}
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	p := &Proxy{Name: "private proxy", Protocol: "http", Host: "proxy.example", Port: 8080, AuthUser: "private-user", AuthPass: "private-password", Enabled: true}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	m := newTestMonitor(t, s, "proxy policy")
	m.AgentEnabled = true
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
	return m, a, p
}

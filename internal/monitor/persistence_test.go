package monitor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

type checkFailureStore struct {
	*storage.SQLiteStore
	readError, writeError error
	writes                int
}

func (s *checkFailureStore) GetMonitorStatus(ctx context.Context, id int64) (*storage.MonitorStatus, error) {
	if s.readError != nil {
		return nil, s.readError
	}
	return s.SQLiteStore.GetMonitorStatus(ctx, id)
}

func (s *checkFailureStore) RecordMonitorCheck(ctx context.Context, mon *storage.Monitor, result *storage.CheckResult, status *storage.MonitorStatus) error {
	s.writes++
	if s.writeError != nil {
		return s.writeError
	}
	return s.SQLiteStore.RecordMonitorCheck(ctx, mon, result, status)
}

func TestFailedCheckCommitDoesNotAdvanceState(t *testing.T) {
	for _, source := range []string{"local", "agent"} {
		t.Run(source, func(t *testing.T) {
			s := &checkFailureStore{SQLiteStore: testStore(t)}
			logger := discardLogger()
			p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
			m := createTestMonitor(t, s, &storage.Monitor{FailureThreshold: 2, AgentEnabled: true})
			agentID := assignTestAgents(t, s, m, 1)[0]
			ctx := context.Background()
			if err := s.UpsertMonitorStatus(ctx, &storage.MonitorStatus{MonitorID: m.ID, ConfigRevision: m.ConfigRevision, Status: "pending", LastCertFingerprint: "previous"}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			apply := func(checkedAt time.Time, wantError bool) {
				t.Helper()
				if source == "local" {
					p.handleResult(ctx, WorkerResult{Monitor: m, Result: &checker.Result{Status: "down", CertFingerprint: "changed"}})
					return
				}
				cr := &storage.CheckResult{MonitorID: m.ID, AgentID: &agentID, Status: "down", CreatedAt: checkedAt, CertFingerprint: "changed"}
				before := *cr
				err := p.ProcessAgentResult(ctx, m, cr)
				if (err != nil) != wantError {
					t.Fatalf("agent error=%v wantError=%v", err, wantError)
				}
				if wantError && !reflect.DeepEqual(before, *cr) {
					t.Fatal("rejected agent result mutated")
				}
			}
			s.writeError = errors.New("injected write failure")
			apply(now, true)
			if events := drainEvents(p); len(events) != 0 {
				t.Fatalf("failed persistence notified: %v", events)
			}
			if _, ok := p.sources.Load(m.ID); ok {
				t.Fatal("failed persistence advanced source cache")
			}
			s.writeError = nil
			// Older than the failed observation: failure must not poison ordering.
			apply(now.Add(-time.Minute), false)
			st, err := s.GetMonitorStatus(ctx, m.ID)
			if err != nil || st.ConsecFails != 1 {
				t.Fatalf("failed result counted or blocked retry: %+v %v", st, err)
			}
			if events := drainEvents(p); len(events) != 1 || countEvents(events, "cert.changed") != 1 {
				t.Fatalf("first commit should notify only the certificate change: %v", events)
			}
			apply(now, false)
			if events := drainEvents(p); countEvents(events, "incident.created") != 1 {
				t.Fatalf("committed failure streak did not notify once: %v", events)
			}
		})
	}
}

func TestStatusReadErrorDoesNotInventDefaultState(t *testing.T) {
	s := &checkFailureStore{SQLiteStore: testStore(t), readError: errors.New("injected read failure")}
	logger := discardLogger()
	p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
	m := createTestMonitor(t, s, &storage.Monitor{AgentEnabled: true})
	agentID := assignTestAgents(t, s, m, 1)[0]
	ctx := context.Background()
	p.handleResult(ctx, WorkerResult{Monitor: m, Result: &checker.Result{Status: "down"}})
	if err := p.ProcessAgentResult(ctx, m, &storage.CheckResult{MonitorID: m.ID, AgentID: &agentID, Status: "down"}); !errors.Is(err, s.readError) {
		t.Fatalf("read failure lost: %v", err)
	}
	if s.writes != 0 {
		t.Fatalf("unreadable status caused %d writes", s.writes)
	}
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("unreadable state notified: %v", events)
	}
}

func TestOldJobAndRevokedLocationCannotDriveCurrentState(t *testing.T) {
	s, p := newCountingPipeline(t)
	ctx := context.Background()
	m := createTestMonitor(t, s, &storage.Monitor{AgentEnabled: true})
	agentID := assignTestAgents(t, s, m, 1)[0]
	oldJob := *m
	processTestAgentResult(t, p, ctx, m, &storage.CheckResult{MonitorID: m.ID, AgentID: &agentID, Status: "down"})
	drainEvents(p)
	if err := s.SetMonitorAgentIDs(ctx, m.ID, nil); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.handleResult(ctx, WorkerResult{Monitor: &oldJob, Result: &checker.Result{Status: "down"}})
	if err := p.ProcessAgentResult(ctx, &oldJob, &storage.CheckResult{MonitorID: m.ID, AgentID: &agentID, Status: "up"}); !errors.Is(err, storage.ErrStaleMonitorCheck) {
		t.Fatalf("revoked location accepted: %v", err)
	}
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("obsolete job notified: %v", events)
	}
	p.handleResult(ctx, WorkerResult{Monitor: current, Result: &checker.Result{Status: "up"}})
	status, err := s.GetMonitorStatus(ctx, m.ID)
	if err != nil || status.Status != "up" || status.ConsecSuccesses != 1 {
		t.Fatalf("revoked failure still counted: %+v %v", status, err)
	}
	if events := drainEvents(p); countEvents(events, "incident.resolved") != 1 {
		t.Fatalf("current healthy location did not resolve: %v", events)
	}
	checks, err := s.ListCheckResults(ctx, m.ID, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil || checks.Total != 2 {
		t.Fatalf("stale jobs entered history: %+v %v", checks, err)
	}
}

func TestConfigurationEditBeforeFirstCheckResetsPersistedStreak(t *testing.T) {
	s, p := newCountingPipeline(t)
	ctx := context.Background()
	m := createTestMonitor(t, s, &storage.Monitor{FailureThreshold: 3})
	// This is the state restored by migration40 before this process has seen
	// any source. An edit must not carry that old configuration's streak.
	if err := s.UpsertMonitorStatus(ctx, &storage.MonitorStatus{MonitorID: m.ID, ConfigRevision: m.ConfigRevision, Status: "down", ConsecFails: 2}); err != nil {
		t.Fatal(err)
	}
	m.Target += "/new-target"
	if err := s.UpdateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	p.handleResult(ctx, WorkerResult{Monitor: m, Result: &checker.Result{Status: "down"}})
	status, err := s.GetMonitorStatus(ctx, m.ID)
	if err != nil || status.ConsecFails != 1 || status.ConfigRevision != m.ConfigRevision {
		t.Fatalf("old configuration streak carried into first result: %+v %v", status, err)
	}
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("old streak caused notification: %v", events)
	}
}

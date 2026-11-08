package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/checker"
	"github.com/y0f/asura/v2/internal/incident"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestMaintenanceAnnouncementSurvivesRestart(t *testing.T) {
	for _, kind := range []string{"http", "manual", "heartbeat"} {
		for _, recoverBeforeAnnouncement := range []bool{false, true} {
			name := kind + "/continued-outage"
			if recoverBeforeAnnouncement {
				name = kind + "/silent-recovery"
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "restart.db")
				db, err := storage.NewSQLiteStore(path, 1)
				if err != nil {
					t.Fatal(err)
				}
				s := &countingStore{SQLiteStore: db}
				t.Cleanup(func() { s.SQLiteStore.Close() })
				maintenance := true
				s.maintenanceFunc = func(int64) bool { return maintenance }
				ctx := context.Background()
				policyID := createTestPolicy(t, s)
				mon := createTestMonitor(t, s, &storage.Monitor{Type: kind, ResendInterval: 0, EscalationPolicyID: &policyID})
				if kind == "heartbeat" {
					if err := s.CreateHeartbeat(ctx, &storage.Heartbeat{MonitorID: mon.ID, Token: "restart-heartbeat", Status: "down"}); err != nil {
						t.Fatal(err)
					}
					last := time.Now().Add(-time.Hour)
					s.expired = []*storage.Heartbeat{{MonitorID: mon.ID, Status: "down", LastPingAt: &last}}
				}
				logger := discardLogger()
				newPipeline := func() *Pipeline {
					return NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
				}
				apply := func(p *Pipeline, status string) {
					t.Helper()
					switch kind {
					case "manual":
						if err := p.ProcessManualStatus(ctx, mon, status, "manual transition"); err != nil {
							t.Fatal(err)
						}
					case "heartbeat":
						if status == "up" {
							if err := p.ProcessHeartbeatPing(ctx, mon.ID, "restart-heartbeat"); err != nil {
								t.Fatal(err)
							}
						} else {
							NewHeartbeatWatcher(s, incident.NewManager(s, logger), p, time.Second, logger).check(ctx)
						}
					default:
						p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: status, Message: "checked transition"}})
					}
				}
				p := newPipeline()
				apply(p, "down")
				inc, err := s.GetOpenIncident(ctx, mon.ID)
				if err != nil || !inc.AnnouncementPending || !inc.EscalationPending || len(drainEvents(p)) != 0 {
					t.Fatalf("maintenance did not persist a silent incident: %+v %v", inc, err)
				}
				if err := s.SQLiteStore.Close(); err != nil {
					t.Fatal(err)
				}
				db, err = storage.NewSQLiteStore(path, 1)
				if err != nil {
					t.Fatal(err)
				}
				s.SQLiteStore = db
				maintenance = false
				p = newPipeline()
				if recoverBeforeAnnouncement {
					apply(p, "up")
					stored, err := s.GetIncident(ctx, inc.ID)
					if err != nil || stored.Status != "resolved" || stored.AnnouncementPending || stored.EscalationPending || len(drainEvents(p)) != 0 {
						t.Fatalf("silent recovery announced or remained pending: %+v %v", stored, err)
					}
					return
				}
				if kind == "manual" {
					// Manual monitors receive no scheduled check or second user write.
					p.reconcilePendingBatch(ctx, 0)
				} else {
					apply(p, "down")
				}
				stored, err := s.GetIncident(ctx, inc.ID)
				if err != nil || stored.AnnouncementPending || stored.EscalationPending || countEvents(drainEvents(p), "incident.created") != 1 {
					t.Fatalf("restart lost deferred announcement: %+v %v", stored, err)
				}
				if _, err := s.GetEscalationStateByIncident(ctx, inc.ID); err != nil {
					t.Fatalf("deferred announcement did not start escalation: %v", err)
				}
				p = newPipeline()
				apply(p, "down")
				if events := drainEvents(p); len(events) != 0 {
					t.Fatalf("restart repeated an acknowledged announcement: %v", events)
				}
			})
		}
	}
}

func TestPipelineReconcilesManualAnnouncementAtStartup(t *testing.T) {
	s, p := newCountingPipeline(t)
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "manual"})
	ctx := context.Background()
	s.maintenanceFunc = func(int64) bool { return true }
	if err := p.ProcessManualStatus(ctx, mon, "down", "maintenance outage"); err != nil {
		t.Fatal(err)
	}
	s.maintenanceFunc = func(int64) bool { return false }
	logger := discardLogger()
	restarted := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { restarted.Run(runCtx); close(done) }()
	select {
	case event := <-restarted.NotifyChan():
		if event.EventType != "incident.created" {
			t.Errorf("startup event = %s", event.EventType)
		}
	case <-time.After(2 * time.Second):
		t.Error("startup did not announce manual outage without another status write")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline did not join pending-announcement worker")
	}
}

func TestPendingReconciliationSkipsBusyMonitor(t *testing.T) {
	s, p := newCountingPipeline(t)
	ctx := context.Background()
	mon := createTestMonitor(t, s, &storage.Monitor{Type: "manual"})
	s.maintenanceFunc = func(int64) bool { return true }
	if err := p.ProcessManualStatus(ctx, mon, "down", "outage"); err != nil {
		t.Fatal(err)
	}
	s.maintenanceFunc = func(int64) bool { return false }
	unlock := p.lockMonitorStatus(mon.ID)
	done := make(chan struct{})
	go func() { p.reconcilePendingBatch(ctx, 0); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		unlock()
		<-done
		t.Fatal("reconciliation waited on an in-flight monitor transition")
	}
	unlock()
	if events := drainEvents(p); len(events) != 0 {
		t.Fatalf("busy monitor was announced: %v", events)
	}
	p.reconcilePendingBatch(ctx, 0)
	if events := drainEvents(p); countEvents(events, "incident.created") != 1 {
		t.Fatalf("later batch did not retry skipped monitor: %v", events)
	}
}

type announcementFailureStore struct {
	*storage.SQLiteStore
	failMark, failEscalation bool
}

func (s *announcementFailureStore) MarkIncidentAnnounced(ctx context.Context, id int64) error {
	if s.failMark {
		return errors.New("injected announcement write failure")
	}
	return s.SQLiteStore.MarkIncidentAnnounced(ctx, id)
}

func (s *announcementFailureStore) PrepareIncidentEscalation(ctx context.Context, id int64, state *storage.EscalationState) error {
	if s.failEscalation {
		return errors.New("injected escalation write failure")
	}
	return s.SQLiteStore.PrepareIncidentEscalation(ctx, id, state)
}

func TestAnnouncementFailuresLeaveRetryableState(t *testing.T) {
	for _, failure := range []string{"queue", "mark", "escalation"} {
		t.Run(failure, func(t *testing.T) {
			s := &announcementFailureStore{SQLiteStore: testStore(t), failMark: failure == "mark", failEscalation: failure == "escalation"}
			ctx := context.Background()
			policyID := createTestPolicy(t, s)
			mon := createTestMonitor(t, s, &storage.Monitor{EscalationPolicyID: &policyID})
			logger := discardLogger()
			mgr := incident.NewManager(s, logger)
			p := NewPipeline(s, checker.NewRegistry(), mgr, 1, false, logger)
			inc, _, err := mgr.ProcessFailure(ctx, mon.ID, mon.Name, "down", "failure")
			if err != nil {
				t.Fatal(err)
			}
			attemptCtx := ctx
			if failure == "queue" {
				for range cap(p.notifyChan) {
					p.notifyChan <- NotificationEvent{}
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				attemptCtx = cancelled
			}
			p.announceIncident(attemptCtx, mon, inc)
			stored, err := s.GetIncident(ctx, inc.ID)
			if err != nil || (failure != "escalation" && !stored.AnnouncementPending) || (failure == "escalation" && (stored.AnnouncementPending || !stored.EscalationPending)) {
				t.Fatalf("failed announcement lost its retry marker: %+v %v", stored, err)
			}
			drainEvents(p)
			s.failMark, s.failEscalation = false, false
			if err := p.processFailure(ctx, mon, "down", "still down", false); err != nil {
				t.Fatal(err)
			}
			stored, err = s.GetIncident(ctx, inc.ID)
			wantEvents := 1
			if failure == "escalation" {
				wantEvents = 0 // Retrying escalation must not resend the initial alert.
			}
			if err != nil || stored.AnnouncementPending || stored.EscalationPending || countEvents(drainEvents(p), "incident.created") != wantEvents {
				t.Fatalf("retry did not finish announcement: %+v %v", stored, err)
			}
			if _, err := s.GetEscalationStateByIncident(ctx, inc.ID); err != nil {
				t.Fatalf("retry failed to preserve/start escalation: %v", err)
			}
		})
	}
}

func TestEscalationFailureDoesNotSuppressRecovery(t *testing.T) {
	s := &announcementFailureStore{SQLiteStore: testStore(t), failEscalation: true}
	ctx := context.Background()
	policyID := createTestPolicy(t, s)
	mon := createTestMonitor(t, s, &storage.Monitor{EscalationPolicyID: &policyID})
	logger := discardLogger()
	p := NewPipeline(s, checker.NewRegistry(), incident.NewManager(s, logger), 1, false, logger)
	p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "down"}})
	if events := drainEvents(p); countEvents(events, "incident.created") != 1 {
		t.Fatalf("expected initial announcement: %v", events)
	}
	p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up"}})
	if events := drainEvents(p); countEvents(events, "incident.resolved") != 1 {
		t.Fatalf("failed escalation suppressed recovery for announced incident: %v", events)
	}
}

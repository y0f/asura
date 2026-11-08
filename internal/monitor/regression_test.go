package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

// countingStore wraps the real SQLite store so individual calls can be counted,
// captured or overridden.
type countingStore struct {
	*storage.SQLiteStore

	maintCalls      atomic.Int64
	openIncCalls    atomic.Int64
	latestCRCalls   atomic.Int64
	latestCRBytes   atomic.Int64
	getStatusCalls  atomic.Int64
	maintenanceFunc func(n int64) bool

	baselineAvg, baselineStddev float64

	mu       sync.Mutex
	inserted []*storage.CheckResult

	expired []*storage.Heartbeat
}

func (s *countingStore) IsMonitorInMaintenance(ctx context.Context, id int64, at time.Time) (bool, error) {
	n := s.maintCalls.Add(1)
	if s.maintenanceFunc != nil {
		return s.maintenanceFunc(n), nil
	}
	return s.SQLiteStore.IsMonitorInMaintenance(ctx, id, at)
}

func (s *countingStore) GetOpenIncident(ctx context.Context, id int64) (*storage.Incident, error) {
	s.openIncCalls.Add(1)
	return s.SQLiteStore.GetOpenIncident(ctx, id)
}

func (s *countingStore) GetLatestCheckResult(ctx context.Context, id int64) (*storage.CheckResult, error) {
	s.latestCRCalls.Add(1)
	cr, err := s.SQLiteStore.GetLatestCheckResult(ctx, id)
	if cr != nil {
		s.latestCRBytes.Add(int64(len(cr.Body)))
	}
	return cr, err
}

func (s *countingStore) GetMonitorStatus(ctx context.Context, id int64) (*storage.MonitorStatus, error) {
	s.getStatusCalls.Add(1)
	ms, err := s.SQLiteStore.GetMonitorStatus(ctx, id)
	if err == nil && ms != nil && s.baselineAvg > 0 {
		ms.BaselineAvg = s.baselineAvg
		ms.BaselineStddev = s.baselineStddev
	}
	return ms, err
}

func (s *countingStore) RecordMonitorCheck(ctx context.Context, mon *storage.Monitor, cr *storage.CheckResult, status *storage.MonitorStatus) error {
	if err := s.SQLiteStore.RecordMonitorCheck(ctx, mon, cr, status); err != nil {
		return err
	}
	s.mu.Lock()
	cp := *cr
	s.inserted = append(s.inserted, &cp)
	s.mu.Unlock()
	return nil
}

func (s *countingStore) ListExpiredHeartbeats(ctx context.Context) ([]*storage.Heartbeat, error) {
	if s.expired != nil {
		return s.expired, nil
	}
	return s.SQLiteStore.ListExpiredHeartbeats(ctx)
}

func (s *countingStore) GetHeartbeatByMonitorID(ctx context.Context, id int64) (*storage.Heartbeat, error) {
	for _, hb := range s.expired {
		if hb.MonitorID == id {
			cp := *hb
			return &cp, nil
		}
	}
	return s.SQLiteStore.GetHeartbeatByMonitorID(ctx, id)
}

func (s *countingStore) ExpireHeartbeat(ctx context.Context, id int64) (bool, error) {
	// This fixture supplies synthetic expired snapshots to test escalation. Real
	// deadline checks and atomic status writes have separate SQLite tests.
	for _, hb := range s.expired {
		if hb.MonitorID == id {
			err := s.SQLiteStore.UpdateHeartbeatStatus(ctx, id, "down")
			return err == nil, err
		}
	}
	return s.SQLiteStore.ExpireHeartbeat(ctx, id)
}

func newCountingPipeline(t *testing.T) (*countingStore, *Pipeline) {
	t.Helper()
	ps := &countingStore{SQLiteStore: testStore(t)}
	logger := discardLogger()
	p := NewPipeline(ps, checker.NewRegistry(), incident.NewManager(ps, logger), 1, false, logger)
	return ps, p
}

func createTestMonitor(t *testing.T, s storage.Store, m *storage.Monitor) *storage.Monitor {
	t.Helper()
	if m.Name == "" {
		m.Name = "proof"
	}
	if m.Type == "" {
		m.Type = "http"
	}
	if m.Target == "" {
		m.Target = "https://example.com"
	}
	if m.Interval == 0 {
		m.Interval = 60
	}
	if m.Timeout == 0 {
		m.Timeout = 10
	}
	if m.FailureThreshold == 0 {
		m.FailureThreshold = 1
	}
	if m.SuccessThreshold == 0 {
		m.SuccessThreshold = 1
	}
	m.Enabled = true
	if err := s.CreateMonitor(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func drainEvents(p *Pipeline) []string {
	var out []string
	for {
		select {
		case ev := <-p.notifyChan:
			out = append(out, ev.EventType)
		default:
			return out
		}
	}
}

func assignTestAgents(t *testing.T, s storage.Store, mon *storage.Monitor, count int) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, count)
	for i := range ids {
		a := &storage.Agent{Name: "test location", Enabled: true}
		if err := s.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		ids[i] = a.ID
	}
	if err := s.SetMonitorAgentIDs(ctx, mon.ID, ids); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	*mon = *current
	return ids
}

func processTestAgentResult(t *testing.T, p *Pipeline, ctx context.Context, mon *storage.Monitor, result *storage.CheckResult) {
	t.Helper()
	if err := p.ProcessAgentResult(ctx, mon, result); err != nil {
		t.Fatal(err)
	}
}

func countEvents(evs []string, typ string) int {
	n := 0
	for _, e := range evs {
		if e == typ {
			n++
		}
	}
	return n
}

func createTestPolicy(t *testing.T, s storage.Store) int64 {
	t.Helper()
	ctx := context.Background()
	ep := &storage.EscalationPolicy{Name: "p", Enabled: true}
	if err := s.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceEscalationPolicySteps(ctx, ep.ID, []*storage.EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{}},
	}); err != nil {
		t.Fatal(err)
	}
	return ep.ID
}

// Regression: agent results bypass upside_down / assertions.
func TestAgentResultAppliesUpsideDownAndAssertions(t *testing.T) {
	ctx := context.Background()

	t.Run("upside_down", func(t *testing.T) {
		ps, p := newCountingPipeline(t)
		mon := createTestMonitor(t, ps, &storage.Monitor{UpsideDown: true, AgentEnabled: true})
		agentID := assignTestAgents(t, ps, mon, 1)[0]

		// Local path: target unreachable -> upside-down monitor is up.
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "down", Message: "refused"}})
		st, _ := ps.SQLiteStore.GetMonitorStatus(ctx, mon.ID)
		if st.Status != "up" {
			t.Fatalf("precondition: local path should be up, got %s", st.Status)
		}
		drainEvents(p)

		// Agent sees the same unreachable target and posts "down".
		processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, AgentID: &agentID, Status: "down", Message: "refused"})
		st, _ = ps.SQLiteStore.GetMonitorStatus(ctx, mon.ID)
		inc, _ := ps.SQLiteStore.GetOpenIncident(ctx, mon.ID)
		if st.Status != "up" || inc != nil {
			t.Fatalf("agent result ignored upside_down: status=%s openIncident=%v", st.Status, inc != nil)
		}
	})

	t.Run("assertions", func(t *testing.T) {
		ps, p := newCountingPipeline(t)
		asr := `{"operator":"and","groups":[{"operator":"and","conditions":[{"type":"status_code","operator":"eq","value":"200"}]}]}`
		mon := createTestMonitor(t, ps, &storage.Monitor{Assertions: json.RawMessage(asr), AgentEnabled: true})
		agentID := assignTestAgents(t, ps, mon, 1)[0]

		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 500}})
		if inc, _ := ps.SQLiteStore.GetOpenIncident(ctx, mon.ID); inc == nil {
			t.Fatal("precondition: local assertion failure should open incident")
		}
		processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, AgentID: &agentID, Status: "up", StatusCode: 500})
		st, _ := ps.SQLiteStore.GetMonitorStatus(ctx, mon.ID)
		inc, _ := ps.SQLiteStore.GetOpenIncident(ctx, mon.ID)
		if st.Status != "down" || inc == nil {
			t.Fatalf("agent 500 bypassed status_code assertion: status=%s openIncident=%v", st.Status, inc != nil)
		}
	})
}

// Regression: local worker and agents share one counter -> incidents oscillate.
func TestAgentAndLocalStreaksDoNotFlap(t *testing.T) {
	ctx := context.Background()
	ps, p := newCountingPipeline(t)
	mon := createTestMonitor(t, ps, &storage.Monitor{FailureThreshold: 2, SuccessThreshold: 1, AgentEnabled: true})
	agentID := assignTestAgents(t, ps, mon, 1)[0]

	var evs []string
	for round := 0; round < 3; round++ {
		processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, AgentID: &agentID, Status: "down", Message: "regional"})
		processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, AgentID: &agentID, Status: "down", Message: "regional"})
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 200}})
		evs = append(evs, drainEvents(p)...)
	}
	created, resolved := countEvents(evs, "incident.created"), countEvents(evs, "incident.resolved")
	if created > 1 || resolved > 1 {
		t.Fatalf("shared counter flapping: %d incidents created, %d resolved in 3 minutes of steady split-view state", created, resolved)
	}
}

// Regression: edited interval is never applied by the scheduler.
func TestSchedulerAppliesEditedInterval(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	mon := createTestMonitor(t, store, &storage.Monitor{Interval: 3600})

	s := NewScheduler(store, make(chan Job, 10), discardLogger())
	s.loadMonitors(ctx)
	now := time.Now()
	s.dispatch(now)

	mon.Interval = 30
	if err := store.UpdateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	s.loadMonitors(ctx)

	s.mu.RLock()
	iv := s.interval(mon.ID, 30)
	next := s.entries[mon.ID].nextRun
	s.mu.RUnlock()

	if time.Duration(iv) != 30*time.Second {
		t.Errorf("effective interval after edit = %v, want 30s", time.Duration(iv))
	}
	if wait := time.Duration(next - now.UnixNano()); wait > 30*time.Second {
		t.Errorf("next run still %v away after lowering interval to 30s", wait)
	}
}

// Regression: degraded assertion downgrades a hard down.
func TestDegradedAssertionKeepsHardDown(t *testing.T) {
	asr := `{"operator":"and","groups":[{"operator":"and","conditions":[{"type":"status_code","operator":"eq","value":"200","degraded":true}]}]}`
	mon := &storage.Monitor{Assertions: json.RawMessage(asr)}
	got := evaluateAssertions(mon, &checker.Result{Status: "down", StatusCode: 0, Message: "connection refused"})
	if got != "down" {
		t.Fatalf("unreachable target with degraded assertion evaluated to %q, want down", got)
	}
}

// Regression: anomaly degraded is not persisted in check_results and never drives incidents.
func TestAnomalyDegradedPersistedAndOpensIncident(t *testing.T) {
	ctx := context.Background()
	ps, p := newCountingPipeline(t)
	ps.baselineAvg, ps.baselineStddev = 100, 10
	mon := createTestMonitor(t, ps, &storage.Monitor{AnomalySensitivity: "high", FailureThreshold: 2})

	for i := 0; i < 3; i++ {
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 200, ResponseTime: 500}})
	}
	st, _ := ps.SQLiteStore.GetMonitorStatus(ctx, mon.ID)
	if st.Status != "degraded" {
		t.Fatalf("precondition: monitor_status should be degraded, got %s", st.Status)
	}
	ps.mu.Lock()
	last := ps.inserted[len(ps.inserted)-1]
	ps.mu.Unlock()
	if last.Status != st.Status {
		t.Errorf("check_results row stored status %q (msg %q) while monitor_status is %q", last.Status, last.Message, st.Status)
	}
	if inc, _ := ps.SQLiteStore.GetOpenIncident(ctx, mon.ID); inc == nil {
		t.Errorf("3 consecutive anomalous (degraded) checks with failure_threshold=2 opened no incident (consec_fails=%d consec_successes=%d)", st.ConsecFails, st.ConsecSuccesses)
	}
}

// Regression: avoidable DB round-trips per healthy check.
func TestHealthyCheckAvoidsExtraQueries(t *testing.T) {
	ctx := context.Background()
	ps, p := newCountingPipeline(t)
	mon := createTestMonitor(t, ps, &storage.Monitor{TrackChanges: true, AnomalySensitivity: "low"})
	body := strings.Repeat("x", 1<<20)
	hash := HashBody(body)

	const n = 200
	for i := 0; i < n; i++ {
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 200, Body: body, BodyHash: hash}})
	}
	t.Logf("%d steady up checks: IsMonitorInMaintenance=%d GetOpenIncident=%d GetMonitorStatus=%d GetLatestCheckResult=%d (%d MB body read)",
		n, ps.maintCalls.Load(), ps.openIncCalls.Load(), ps.getStatusCalls.Load(), ps.latestCRCalls.Load(), ps.latestCRBytes.Load()>>20)
	if ps.maintCalls.Load() >= n || ps.openIncCalls.Load() >= n || ps.latestCRCalls.Load() >= n-1 || ps.getStatusCalls.Load() > n {
		t.Fatalf("steady healthy monitor (no status change, no open incident, unchanged hash) still does per-check maintenance/open-incident/latest-body/status re-reads")
	}
}

func BenchmarkHandleResultSteadyUp(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	store, err := storage.NewSQLiteStore(dir+"/b.db", 2)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	logger := discardLogger()
	p := NewPipeline(store, checker.NewRegistry(), incident.NewManager(store, logger), 1, false, logger)
	mon := &storage.Monitor{Name: "b", Type: "http", Target: "https://example.com", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1, TrackChanges: true}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		b.Fatal(err)
	}
	body := strings.Repeat("x", 1<<20)
	hash := HashBody(body)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 200, Body: body, BodyHash: hash}})
	}
}

// Regression: incident opened during maintenance is never announced/escalated.
func TestIncidentOpenedInMaintenanceAnnouncedAfterwards(t *testing.T) {
	ctx := context.Background()
	ps, p := newCountingPipeline(t)
	ps.maintenanceFunc = func(n int64) bool { return n == 1 }
	pid := createTestPolicy(t, ps)
	mon := createTestMonitor(t, ps, &storage.Monitor{FailureThreshold: 1, ResendInterval: 0, EscalationPolicyID: &pid})

	var evs []string
	for i := 0; i < 10; i++ {
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "down", Message: "refused"}})
		evs = append(evs, drainEvents(p)...)
	}
	inc, _ := ps.SQLiteStore.GetOpenIncident(ctx, mon.ID)
	if inc == nil {
		t.Fatal("precondition: incident should be open")
	}
	_, escErr := ps.SQLiteStore.GetEscalationStateByIncident(ctx, inc.ID)

	p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 200}})
	evs = append(evs, drainEvents(p)...)

	created, resolved := countEvents(evs, "incident.created"), countEvents(evs, "incident.resolved")
	if created == 0 || escErr != nil {
		t.Fatalf("9 failing checks after maintenance ended: incident.created=%d escalation_state err=%v; on recovery incident.resolved=%d (resolved for an incident never announced)", created, escErr, resolved)
	}
}

// Regression: heartbeat monitors never start escalation.
func TestHeartbeatStartsEscalation(t *testing.T) {
	ctx := context.Background()
	ps, p := newCountingPipeline(t)
	pid := createTestPolicy(t, ps)
	mon := createTestMonitor(t, ps, &storage.Monitor{Type: "heartbeat", Target: "heartbeat", EscalationPolicyID: &pid})
	if err := ps.CreateHeartbeat(ctx, &storage.Heartbeat{MonitorID: mon.ID, Token: "tok", Grace: 0, Status: "up"}); err != nil {
		t.Fatal(err)
	}
	last := time.Now().Add(-time.Hour)
	ps.expired = []*storage.Heartbeat{{MonitorID: mon.ID, Status: "up", LastPingAt: &last}}

	w := NewHeartbeatWatcher(ps, incident.NewManager(ps, discardLogger()), p, time.Second, discardLogger())
	w.check(ctx)

	evs := drainEvents(p)
	if countEvents(evs, "incident.created") != 1 {
		t.Fatalf("precondition: expected incident.created, got %v", evs)
	}
	inc, _ := ps.SQLiteStore.GetOpenIncident(ctx, mon.ID)
	if inc == nil {
		t.Fatal("precondition: incident should be open")
	}
	if _, err := ps.SQLiteStore.GetEscalationStateByIncident(ctx, inc.ID); err != nil {
		pending, _ := ps.SQLiteStore.ListPendingEscalationStates(ctx, time.Now().Add(time.Hour))
		t.Fatalf("heartbeat incident with escalation policy has no escalation state (err=%v, pending states=%d)", err, len(pending))
	}
}

// A failure seen by the agents alone still reaches the failure threshold even
// though the local worker keeps reporting up in between, and recovery waits
// until every location is up.
func TestAgentOnlyOutageStillAlerts(t *testing.T) {
	ctx := context.Background()
	ps, p := newCountingPipeline(t)
	mon := createTestMonitor(t, ps, &storage.Monitor{FailureThreshold: 3, SuccessThreshold: 1, AgentEnabled: true})
	agents := assignTestAgents(t, ps, mon, 2)
	a1, a2 := agents[0], agents[1]

	var evs []string
	for i := 0; i < 3; i++ {
		processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, Status: "down", AgentID: &a1})
		processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, Status: "down", AgentID: &a2})
		p.handleResult(ctx, WorkerResult{Monitor: mon, Result: &checker.Result{Status: "up", StatusCode: 200}})
		evs = append(evs, drainEvents(p)...)
	}
	if countEvents(evs, "incident.created") != 1 || countEvents(evs, "incident.resolved") != 0 {
		t.Fatalf("regional outage: events %v, want exactly one incident.created", evs)
	}

	processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, Status: "up", AgentID: &a1})
	if evs := drainEvents(p); countEvents(evs, "incident.resolved") != 0 {
		t.Fatal("resolved while agent 2 still reports down")
	}
	processTestAgentResult(t, p, ctx, mon, &storage.CheckResult{MonitorID: mon.ID, Status: "up", AgentID: &a2})
	if evs := drainEvents(p); countEvents(evs, "incident.resolved") != 1 {
		t.Fatalf("all locations up: events %v, want incident.resolved", evs)
	}
}

func TestAgentAssertionsDropUnavailableInputs(t *testing.T) {
	tests := []struct {
		name, in string
		code     int
		want     string
	}{
		{"body-only assertion is skipped", `{"groups":[{"conditions":[{"type":"body_contains","value":"ok"}]}]}`, 200, "up"},
		{"status code still checked", `{"groups":[{"conditions":[{"type":"body_contains","value":"ok"},{"type":"status_code","operator":"eq","value":"200"}]}]}`, 500, "down"},
		{"or-group with body condition passes", `{"groups":[{"operator":"or","conditions":[{"type":"body_contains","value":"ok"},{"type":"status_code","operator":"eq","value":"200"}]}]}`, 500, "up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mon := &storage.Monitor{Assertions: json.RawMessage(tt.in)}
			cr := &storage.CheckResult{Status: "up", StatusCode: tt.code}
			if got := evaluateAgentResult(mon, cr, &storage.MonitorStatus{}); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

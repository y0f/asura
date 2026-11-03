package storage

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAgentCRUD(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	t.Run("create agent", func(t *testing.T) {
		a := &Agent{Name: "US-East", Location: "New York", Enabled: true}
		if err := store.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		if a.ID == 0 {
			t.Fatal("expected non-zero ID")
		}
		if a.Token == "" {
			t.Fatal("expected non-empty token")
		}
		if !a.Enabled {
			t.Fatal("expected enabled")
		}
	})

	t.Run("get by token", func(t *testing.T) {
		a := &Agent{Name: "EU-West", Location: "Frankfurt", Enabled: true}
		store.CreateAgent(ctx, a)

		got, err := store.GetAgentByToken(ctx, a.Token)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "EU-West" {
			t.Fatalf("expected EU-West, got %s", got.Name)
		}
		if got.Location != "Frankfurt" {
			t.Fatalf("expected Frankfurt, got %s", got.Location)
		}
	})

	t.Run("list agents", func(t *testing.T) {
		agents, err := store.ListAgents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(agents) < 2 {
			t.Fatalf("expected at least 2, got %d", len(agents))
		}
	})

	t.Run("update heartbeat", func(t *testing.T) {
		a := &Agent{Name: "AP-South", Location: "Singapore", Enabled: true}
		store.CreateAgent(ctx, a)

		if err := store.UpdateAgentHeartbeat(ctx, a.ID); err != nil {
			t.Fatal(err)
		}

		got, _ := store.GetAgent(ctx, a.ID)
		if got.LastHeartbeat == nil {
			t.Fatal("expected non-nil heartbeat after update")
		}
	})

	t.Run("delete agent", func(t *testing.T) {
		a := &Agent{Name: "ToDelete", Enabled: true}
		store.CreateAgent(ctx, a)

		if _, err := store.DeleteAgent(ctx, a.ID); err != nil {
			t.Fatal(err)
		}

		_, err := store.GetAgent(ctx, a.ID)
		if err == nil {
			t.Fatal("expected error after deletion")
		}
	})

	t.Run("invalid token returns error", func(t *testing.T) {
		_, err := store.GetAgentByToken(ctx, "nonexistent-token")
		if err == nil {
			t.Fatal("expected error for invalid token")
		}
	})
}

func TestListAgentJobs(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{
		Name: "HTTP Test", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}

	heartbeat := &Monitor{
		Name: "HB Test", Type: "heartbeat", Target: "heartbeat",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	store.CreateMonitor(ctx, heartbeat)

	jobs, err := store.ListAgentJobs(ctx, 1) // no assignments: every agent gets these jobs
	if err != nil {
		t.Fatal(err)
	}

	if len(jobs) != 1 {
		t.Fatalf("expected 1 job (excluding heartbeat), got %d", len(jobs))
	}
	if jobs[0].Name != "HTTP Test" {
		t.Fatalf("expected HTTP Test, got %s", jobs[0].Name)
	}
}

func TestListAgentJobsExcludesAgentDisabled(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	agentEnabled := &Monitor{
		Name: "Agent OK", Type: "tcp", Target: "example.com:443",
		Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, agentEnabled); err != nil {
		t.Fatal(err)
	}

	agentDisabled := &Monitor{
		Name: "Agent Off", Type: "tcp", Target: "127.0.0.1:3306",
		Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: false,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, agentDisabled); err != nil {
		t.Fatal(err)
	}

	jobs, err := store.ListAgentJobs(ctx, 1) // no assignments: every agent gets these jobs
	if err != nil {
		t.Fatal(err)
	}

	if len(jobs) != 1 {
		t.Fatalf("expected 1 job (agent_enabled=true only), got %d", len(jobs))
	}
	if jobs[0].Name != "Agent OK" {
		t.Fatalf("expected Agent OK, got %s", jobs[0].Name)
	}
}

func TestAgentEnabledPersistsCorrectly(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	m := &Monitor{
		Name: "Agent Enabled", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AgentEnabled {
		t.Fatal("expected AgentEnabled to be true")
	}

	got.AgentEnabled = false
	if err := store.UpdateMonitor(ctx, got); err != nil {
		t.Fatal(err)
	}

	got2, err := store.GetMonitor(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.AgentEnabled {
		t.Fatal("expected AgentEnabled to be false after update")
	}
}

// Deleting a monitor's only assigned agent must not widen it to every agent.
func TestDeleteAgentDisablesMonitorsAssignedOnlyToIt(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var agents [3]*Agent
	for i := range agents {
		agents[i] = &Agent{Name: string(rune('A' + i)), Enabled: true}
		if err := store.CreateAgent(ctx, agents[i]); err != nil {
			t.Fatal(err)
		}
	}
	a, b, c := agents[0].ID, agents[1].ID, agents[2].ID

	newMon := func(name string, agentIDs ...int64) *Monitor {
		m := &Monitor{
			Name: name, Type: "tcp", Target: "example.com:443",
			Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: true,
			FailureThreshold: 3, SuccessThreshold: 1,
		}
		if err := store.CreateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
		if err := store.SetMonitorAgentIDs(ctx, m.ID, agentIDs); err != nil {
			t.Fatal(err)
		}
		return m
	}
	onlyA := newMon("only-a", a)
	aAndB := newMon("a-and-b", a, b)
	everyAgent := newMon("every-agent")

	disabled, err := store.DeleteAgent(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 1 || disabled[0] != onlyA.ID {
		t.Fatalf("disabled = %v, want [%d]", disabled, onlyA.ID)
	}

	got, err := store.GetMonitor(ctx, onlyA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentEnabled {
		t.Fatal("monitor assigned only to the deleted agent is still enabled for agents")
	}
	for _, id := range []int64{aAndB.ID, everyAgent.ID} {
		m, err := store.GetMonitor(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !m.AgentEnabled {
			t.Fatalf("monitor %q was disabled for agents", m.Name)
		}
	}

	jobIDs := func(agentID int64) map[int64]bool {
		jobs, err := store.ListAgentJobs(ctx, agentID)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[int64]bool{}
		for _, j := range jobs {
			ids[j.ID] = true
		}
		return ids
	}
	if jobs := jobIDs(c); jobs[onlyA.ID] || jobs[aAndB.ID] || !jobs[everyAgent.ID] {
		t.Fatalf("unassigned agent jobs = %v, want only %d", jobs, everyAgent.ID)
	}
	if jobs := jobIDs(b); !jobs[aAndB.ID] || jobs[onlyA.ID] {
		t.Fatalf("agent B jobs = %v, want %d and not %d", jobs, aAndB.ID, onlyA.ID)
	}
}

// Custom headers and bodies often carry credentials, so they are only sent to
// agents a monitor is explicitly assigned to.
func TestAgentJobsWithRequestDataNeedAssignment(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	assignedAgent := &Agent{Name: "assigned", Enabled: true}
	otherAgent := &Agent{Name: "other", Enabled: true}
	for _, ag := range []*Agent{assignedAgent, otherAgent} {
		if err := store.CreateAgent(ctx, ag); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name, typ, settings string
		assign              bool
		offered             bool
	}{
		{"header", "http", `{"headers":{"X-API-Key":"k3y"}}`, false, false},
		{"body", "http", `{"method":"POST","body":"{\"token\":\"t\"}"}`, false, false},
		{"multi-step header", "http_multi", `{"steps":[{"url":"https://example.com","headers":{"Cookie":"s=1"}}]}`, false, false},
		{"multi-step body", "http_multi", `{"steps":[{"url":"https://example.com","body":"pw=1"}]}`, false, false},
		{"empty header value", "http", `{"headers":{"X-Trace":""}}`, false, true},
		{"no request data", "http", `{"method":"GET"}`, false, true},
		{"header, assigned", "http", `{"headers":{"X-API-Key":"k3y"}}`, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Monitor{
				Name: tt.name, Type: tt.typ, Target: "https://example.com",
				Interval: 60, Timeout: 10, Enabled: true, AgentEnabled: true,
				FailureThreshold: 3, SuccessThreshold: 1,
				Settings: json.RawMessage(tt.settings),
			}
			if err := store.CreateMonitor(ctx, m); err != nil {
				t.Fatal(err)
			}
			agentID := otherAgent.ID
			if tt.assign {
				agentID = assignedAgent.ID
				if err := store.SetMonitorAgentIDs(ctx, m.ID, []int64{agentID}); err != nil {
					t.Fatal(err)
				}
			}

			jobs, err := store.ListAgentJobs(ctx, agentID)
			if err != nil {
				t.Fatal(err)
			}
			var job *AgentJob
			for _, j := range jobs {
				if j.ID == m.ID {
					job = j
				}
			}
			if (job != nil) != tt.offered {
				t.Fatalf("offered = %v, want %v", job != nil, tt.offered)
			}
			if ok, err := store.AgentMayCheckMonitor(ctx, agentID, m.ID); err != nil || ok != tt.offered {
				t.Fatalf("AgentMayCheckMonitor = %v, %v; want %v (same rule as ListAgentJobs)", ok, err, tt.offered)
			}
			if job != nil && tt.assign {
				var s map[string]any
				json.Unmarshal(job.Settings, &s)
				if h, _ := s["headers"].(map[string]any); h["X-API-Key"] != "k3y" {
					t.Fatalf("assigned agent did not receive the monitor's headers: %s", job.Settings)
				}
			}
		})
	}
}

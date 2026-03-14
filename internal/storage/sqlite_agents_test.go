package storage

import (
	"context"
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

		if err := store.DeleteAgent(ctx, a.ID); err != nil {
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
		Interval: 60, Timeout: 10, Enabled: true,
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

	jobs, err := store.ListAgentJobs(ctx)
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

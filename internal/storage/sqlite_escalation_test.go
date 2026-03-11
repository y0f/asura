package storage

import (
	"context"
	"testing"
	"time"
)

func TestEscalationPolicyCRUD(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	ep := &EscalationPolicy{
		Name:        "Test Policy",
		Description: "test desc",
		Enabled:     true,
		Repeat:      false,
	}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if ep.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := store.GetEscalationPolicy(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test Policy" {
		t.Fatalf("expected 'Test Policy', got %q", got.Name)
	}
	if got.Description != "test desc" {
		t.Fatalf("expected 'test desc', got %q", got.Description)
	}
	if !got.Enabled {
		t.Fatal("expected enabled")
	}

	policies, err := store.ListEscalationPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}

	ep.Name = "Updated Policy"
	ep.Repeat = true
	if err := store.UpdateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetEscalationPolicy(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Updated Policy" {
		t.Fatalf("expected 'Updated Policy', got %q", got.Name)
	}
	if !got.Repeat {
		t.Fatal("expected repeat=true")
	}

	if err := store.DeleteEscalationPolicy(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	policies, err = store.ListEscalationPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("expected 0 policies after delete, got %d", len(policies))
	}
}

func TestEscalationPolicySteps(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	ep := &EscalationPolicy{Name: "Steps Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}

	steps := []*EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 0, NotificationChannelIDs: []int64{1, 2}},
		{StepOrder: 1, DelayMinutes: 5, NotificationChannelIDs: []int64{3}},
		{StepOrder: 2, DelayMinutes: 15, NotificationChannelIDs: []int64{1, 3}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, steps); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetEscalationPolicySteps(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(got))
	}
	if got[0].DelayMinutes != 0 {
		t.Fatalf("step 0: expected delay 0, got %d", got[0].DelayMinutes)
	}
	if got[1].DelayMinutes != 5 {
		t.Fatalf("step 1: expected delay 5, got %d", got[1].DelayMinutes)
	}
	if len(got[2].NotificationChannelIDs) != 2 {
		t.Fatalf("step 2: expected 2 channel IDs, got %d", len(got[2].NotificationChannelIDs))
	}

	// Replace with fewer steps
	newSteps := []*EscalationPolicyStep{
		{StepOrder: 0, DelayMinutes: 10, NotificationChannelIDs: []int64{5}},
	}
	if err := store.ReplaceEscalationPolicySteps(ctx, ep.ID, newSteps); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetEscalationPolicySteps(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 step after replace, got %d", len(got))
	}
	if got[0].DelayMinutes != 10 {
		t.Fatalf("expected delay 10, got %d", got[0].DelayMinutes)
	}
}

func TestEscalationState(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	ep := &EscalationPolicy{Name: "State Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}

	mon := &Monitor{
		Name: "Test Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	inc := &Incident{
		MonitorID: mon.ID, Status: "open", Cause: "timeout",
	}
	if err := store.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	state := &EscalationState{
		IncidentID:  inc.ID,
		PolicyID:    ep.ID,
		CurrentStep: 0,
		NextFireAt:  now,
	}
	if err := store.CreateEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PolicyID != ep.ID {
		t.Fatalf("expected policy ID %d, got %d", ep.ID, got.PolicyID)
	}

	pending, err := store.ListPendingEscalationStates(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending state, got %d", len(pending))
	}

	// Not yet due
	notDue, err := store.ListPendingEscalationStates(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(notDue) != 0 {
		t.Fatalf("expected 0 pending states before fire time, got %d", len(notDue))
	}

	state.CurrentStep = 1
	state.NextFireAt = now.Add(5 * time.Minute)
	if err := store.UpdateEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}

	got, err = store.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStep != 1 {
		t.Fatalf("expected step 1, got %d", got.CurrentStep)
	}

	if err := store.DeleteEscalationStateByIncident(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	pending, err = store.ListPendingEscalationStates(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending states after delete, got %d", len(pending))
	}
}

func TestMonitorEscalationPolicyAssignment(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	ep := &EscalationPolicy{Name: "Assign Test", Enabled: true}
	if err := store.CreateEscalationPolicy(ctx, ep); err != nil {
		t.Fatal(err)
	}

	mon := &Monitor{
		Name: "Test Mon", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
		EscalationPolicyID: &ep.ID,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EscalationPolicyID == nil {
		t.Fatal("expected escalation policy ID to be set")
	}
	if *got.EscalationPolicyID != ep.ID {
		t.Fatalf("expected escalation policy ID %d, got %d", ep.ID, *got.EscalationPolicyID)
	}

	// Update to remove
	mon.EscalationPolicyID = nil
	if err := store.UpdateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EscalationPolicyID != nil {
		t.Fatal("expected escalation policy ID to be nil after update")
	}
}

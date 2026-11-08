package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func escalationMutationFixture(t *testing.T) (*SQLiteStore, []*NotificationChannel) {
	t.Helper()
	store := testStore(t)
	channels := []*NotificationChannel{{Name: "first", Type: "webhook", Settings: json.RawMessage(`{}`)}, {Name: "second", Type: "webhook", Settings: json.RawMessage(`{}`)}}
	for _, channel := range channels {
		if err := store.CreateNotificationChannel(context.Background(), channel); err != nil {
			t.Fatal(err)
		}
	}
	return store, channels
}
func escalationCandidate(channelID int64) *EscalationPolicy {
	return &EscalationPolicy{Name: "original", Enabled: true, Steps: []*EscalationPolicyStep{{DelayMinutes: 2, NotificationChannelIDs: []int64{channelID}}}}
}

func TestEscalationSaveRollsBackMetadataAndStepsTogether(t *testing.T) {
	store, channels := escalationMutationFixture(t)
	ctx := context.Background()
	trigger := `CREATE TRIGGER reject_escalation_step BEFORE INSERT ON escalation_policy_steps BEGIN SELECT RAISE(ABORT,'injected step failure'); END`
	if _, err := store.writeDB.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	input := escalationCandidate(channels[0].ID)
	if got, err := store.SaveEscalationPolicy(ctx, 0, func(*EscalationPolicy) (*EscalationPolicy, error) { return input, nil }); err == nil || got != nil {
		t.Fatalf("failed create returned success: %+v %v", got, err)
	}
	policies, err := store.ListEscalationPolicies(ctx)
	if err != nil || len(policies) != 0 || input.ID != 0 || input.Steps[0].ID != 0 {
		t.Fatalf("failed create left partial rows/input mutation: %+v %v", policies, err)
	}
	if _, err := store.writeDB.ExecContext(ctx, "DROP TRIGGER reject_escalation_step"); err != nil {
		t.Fatal(err)
	}
	saved, err := store.SaveEscalationPolicy(ctx, 0, func(*EscalationPolicy) (*EscalationPolicy, error) { return input, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	if got, err := store.SaveEscalationPolicy(ctx, saved.ID, func(current *EscalationPolicy) (*EscalationPolicy, error) {
		current.Name = "not saved"
		current.Steps[0].NotificationChannelIDs = []int64{channels[1].ID}
		return current, nil
	}); err == nil || got != nil {
		t.Fatalf("failed update reported success: %+v %v", got, err)
	}
	policy, err := store.GetEscalationPolicy(ctx, saved.ID)
	if err != nil || policy.Name != "original" {
		t.Fatalf("metadata partially changed: %+v %v", policy, err)
	}
	steps, err := store.GetEscalationPolicySteps(ctx, saved.ID)
	if err != nil || len(steps) != 1 || steps[0].NotificationChannelIDs[0] != channels[0].ID || steps[0].ID != saved.Steps[0].ID {
		t.Fatalf("old steps were lost: %+v %v", steps, err)
	}
	// Metadata-only edits do not rewrite unchanged steps or require their INSERT.
	got, err := store.SaveEscalationPolicy(ctx, saved.ID, func(current *EscalationPolicy) (*EscalationPolicy, error) {
		current.Name = "metadata only"
		return current, nil
	})
	if err != nil || got.Steps[0].ID != saved.Steps[0].ID {
		t.Fatalf("metadata update rewrote routing: %+v %v", got, err)
	}
}

func TestEscalationSaveValidatesChannelsBeforeWrites(t *testing.T) {
	store, channels := escalationMutationFixture(t)
	ctx := context.Background()
	for _, ids := range [][]int64{{0}, {-1}, {999999}, {channels[0].ID, channels[0].ID}} {
		input := escalationCandidate(channels[0].ID)
		input.Steps[0].NotificationChannelIDs = ids
		if got, err := store.SaveEscalationPolicy(ctx, 0, func(*EscalationPolicy) (*EscalationPolicy, error) { return input, nil }); !errors.Is(err, ErrInvalidEscalationChannels) || got != nil {
			t.Fatalf("invalid channels%v returned %+v %v", ids, got, err)
		}
	}
	policies, err := store.ListEscalationPolicies(ctx)
	if err != nil || len(policies) != 0 {
		t.Fatalf("invalid channel assignment left policies: %+v %v", policies, err)
	}
}

func TestEscalationOmittedStepsUseCurrentTransactionalSnapshot(t *testing.T) {
	store, channels := escalationMutationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved, err := store.SaveEscalationPolicy(ctx, 0, func(*EscalationPolicy) (*EscalationPolicy, error) { return escalationCandidate(channels[0].ID), nil })
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.writeDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := replaceEscalationPolicySteps(ctx, tx, saved.ID, escalationCandidate(channels[1].ID).Steps); err != nil {
		t.Fatal(err)
	}
	type result struct {
		value *EscalationPolicy
		err   error
	}
	done := make(chan result, 1)
	go func() {
		got, err := store.SaveEscalationPolicy(ctx, saved.ID, func(current *EscalationPolicy) (*EscalationPolicy, error) {
			current.Description = "metadata edit"
			return current, nil
		})
		done <- result{got, err}
	}()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.value.Steps[0].NotificationChannelIDs[0] != channels[1].ID {
			t.Fatalf("omitted steps restored stale assignment: %+v %v", got.value, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

type heldEscalationRead struct {
	escalationDB
	stepsRead, resume chan struct{}
}

func (h heldEscalationRead) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	close(h.stepsRead)
	select {
	case <-h.resume:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return h.escalationDB.QueryContext(ctx, query, args...)
}

func TestEscalationReadKeepsMetadataAndStepsInOneSnapshot(t *testing.T) {
	store, channels := escalationMutationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved, err := store.SaveEscalationPolicy(ctx, 0, func(*EscalationPolicy) (*EscalationPolicy, error) { return escalationCandidate(channels[0].ID), nil })
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	held := heldEscalationRead{escalationDB: tx, stepsRead: make(chan struct{}), resume: make(chan struct{})}
	type result struct {
		policy *EscalationPolicy
		err    error
	}
	done := make(chan result, 1)
	go func() { policy, err := loadEscalationPolicy(ctx, held, saved.ID); done <- result{policy, err} }()
	select {
	case <-held.stepsRead:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = store.SaveEscalationPolicy(ctx, saved.ID, func(current *EscalationPolicy) (*EscalationPolicy, error) {
		current.Name = "new snapshot"
		current.Steps[0].NotificationChannelIDs = []int64{channels[1].ID}
		return current, nil
	})
	close(held.resume)
	select {
	case got := <-done:
		if got.err != nil || got.policy.Name != "original" || got.policy.Steps[0].NotificationChannelIDs[0] != channels[0].ID {
			t.Fatalf("mixed metadata and routing revisions: %+v %v", got.policy, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err != nil {
		t.Fatal(err)
	}
	// Release the old reader before fetching the newly committed snapshot.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	policies, err := store.ListEscalationPolicies(ctx)
	if err != nil || len(policies) != 1 || policies[0].Name != "new snapshot" || len(policies[0].Steps) != 1 || policies[0].Steps[0].NotificationChannelIDs[0] != channels[1].ID {
		t.Fatalf("list did not include current coherent routing: %+v %v", policies, err)
	}
}

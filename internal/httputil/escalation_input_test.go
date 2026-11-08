package httputil

import (
	"errors"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestEscalationInputPreservesHiddenAssignmentsAndSeparatesAuthority(t *testing.T) {
	current := &storage.EscalationPolicy{Name: "current", Enabled: true, Steps: []*storage.EscalationPolicyStep{{DelayMinutes: 2, NotificationChannelIDs: []int64{7}}}}
	name := "renamed"
	input := EscalationPolicyInput{Name: &name}
	got, err := input.Apply(current, false)
	if err != nil || got.Name != name || !got.Enabled || got.Steps[0].NotificationChannelIDs[0] != 7 {
		t.Fatalf("metadata-only edit lost hidden fields: %+v %v", got, err)
	}
	if current.Name != "current" {
		t.Fatal("input mutated current row")
	}
	if _, err := input.Apply(nil, false); !errors.Is(err, ErrEscalationChannelPermission) {
		t.Fatalf("create lacks channel-read gate: %v", err)
	}
	identical := []*storage.EscalationPolicyStep{{DelayMinutes: 2, NotificationChannelIDs: []int64{7}}}
	input.Steps = &identical
	if _, err := input.Apply(current, false); err != nil {
		t.Fatalf("unchanged IDs required new authority: %v", err)
	}
	changed := []*storage.EscalationPolicyStep{{DelayMinutes: 2, NotificationChannelIDs: []int64{8}}}
	input.Steps = &changed
	if _, err := input.Apply(current, false); !errors.Is(err, ErrEscalationChannelPermission) {
		t.Fatalf("channel mutation bypassed read: %v", err)
	}
	if _, err := input.Apply(current, true); err != nil {
		t.Fatal(err)
	}
	for _, steps := range [][]*storage.EscalationPolicyStep{nil, {nil}, {{DelayMinutes: -1, NotificationChannelIDs: []int64{7}}}, {{NotificationChannelIDs: []int64{0}}}, {{NotificationChannelIDs: []int64{7, 7}}}} {
		input.Steps = &steps
		if _, err := input.Apply(current, true); !errors.Is(err, ErrInvalidEscalationPolicy) {
			t.Fatalf("invalid steps accepted or panicked: %+v %v", steps, err)
		}
	}
}

package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

var ErrInvalidEscalationChannels = errors.New("escalation steps must reference existing notification channels using unique positive IDs")

// EscalationPolicyPrepare runs while the writer transaction is held. It must be
// pure and perform no I/O. Current includes the latest steps; nil means create.
type EscalationPolicyPrepare func(current *EscalationPolicy) (*EscalationPolicy, error)

// SameEscalationSteps compares routing behavior, excluding database identities.
func SameEscalationSteps(a, b []*EscalationPolicyStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == nil || b[i] == nil || a[i].DelayMinutes != b[i].DelayMinutes || !slices.Equal(a[i].NotificationChannelIDs, b[i].NotificationChannelIDs) {
			return false
		}
	}
	return true
}

func cloneEscalationPolicy(policy *EscalationPolicy) *EscalationPolicy {
	if policy == nil {
		return nil
	}
	copy := *policy
	copy.Steps = make([]*EscalationPolicyStep, len(policy.Steps))
	for i, step := range policy.Steps {
		if step != nil {
			value := *step
			value.NotificationChannelIDs = slices.Clone(step.NotificationChannelIDs)
			copy.Steps[i] = &value
		}
	}
	return &copy
}

// SaveEscalationPolicy commits metadata and routing steps together. Returning an
// error leaves policy, steps and caller-owned input unchanged.
func (s *SQLStore) SaveEscalationPolicy(ctx context.Context, id int64, prepare EscalationPolicyPrepare) (*EscalationPolicy, error) {
	if id < 0 || prepare == nil {
		return nil, fmt.Errorf("invalid escalation save")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current *EscalationPolicy
	if id != 0 {
		current, err = getEscalationPolicy(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		current.Steps, err = getEscalationPolicySteps(ctx, tx, &id)
		if err != nil {
			return nil, err
		}
	}
	candidate, err := prepare(cloneEscalationPolicy(current))
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, fmt.Errorf("missing prepared escalation policy")
	}
	next := cloneEscalationPolicy(candidate)
	next.ID = id
	changed := current == nil || !SameEscalationSteps(current.Steps, next.Steps)
	if changed {
		ids := map[int64]bool{}
		for _, step := range next.Steps {
			if step == nil || len(step.NotificationChannelIDs) == 0 {
				return nil, ErrInvalidEscalationChannels
			}
			seen := map[int64]bool{}
			for _, channelID := range step.NotificationChannelIDs {
				if channelID <= 0 || seen[channelID] {
					return nil, ErrInvalidEscalationChannels
				}
				seen[channelID] = true
				ids[channelID] = true
			}
		}
		channelIDs := make([]int64, 0, len(ids))
		for channelID := range ids {
			channelIDs = append(channelIDs, channelID)
		}
		slices.Sort(channelIDs)
		for len(channelIDs) > 0 {
			batch := channelIDs[:min(500, len(channelIDs))]
			placeholders, args := bulkArgs(batch)
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_channels WHERE id IN ("+placeholders+")", args...).Scan(&count); err != nil {
				return nil, err
			}
			if count != len(batch) {
				return nil, ErrInvalidEscalationChannels
			}
			channelIDs = channelIDs[len(batch):]
		}
	}
	if current != nil {
		next.CreatedAt = current.CreatedAt
	}
	if err := writeEscalationPolicy(ctx, tx, next, id == 0); err != nil {
		return nil, err
	}
	if changed {
		next.Steps, err = replaceEscalationPolicySteps(ctx, tx, next.ID, next.Steps)
		if err != nil {
			return nil, err
		}
	} else {
		next.Steps = cloneEscalationPolicy(current).Steps
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return next, nil
}
